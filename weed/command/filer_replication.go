package command

import (
	"strings"

	"github.com/chrislusf/seaweedfs/weed/glog"
	"github.com/chrislusf/seaweedfs/weed/replication"
	"github.com/chrislusf/seaweedfs/weed/server"
	"github.com/spf13/viper"
	"github.com/chrislusf/seaweedfs/weed/replication/sink"
	_ "github.com/chrislusf/seaweedfs/weed/replication/sink/s3sink"
	_ "github.com/chrislusf/seaweedfs/weed/replication/sink/filersink"
	_ "github.com/chrislusf/seaweedfs/weed/replication/sink/gcssink"
)

func init() {
	cmdFilerReplicate.Run = runFilerReplicate // break init cycle
}

var cmdFilerReplicate = &Command{
	UsageLine: "filer.replicate",
	Short:     "replicate file changes to another destination",
	Long: `replicate file changes to another destination

	filer.replicate listens on filer notifications. If any file is updated, it will fetch the updated content,
	and write to the other destination.

	Run "weed scaffold -config replication" to generate a replication.toml file and customize the parameters.

  `,
}

func runFilerReplicate(cmd *Command, args []string) bool {

	weed_server.LoadConfiguration("replication", true)
	config := viper.GetViper()

	var notificationInput replication.NotificationInput

	for _, input := range replication.NotificationInputs {
		if config.GetBool("notification." + input.GetName() + ".enabled") {
			viperSub := config.Sub("notification." + input.GetName())
			if err := input.Initialize(viperSub); err != nil {
				glog.Fatalf("Failed to initialize notification input for %s: %+v",
					input.GetName(), err)
			}
			glog.V(0).Infof("Configure notification input to %s", input.GetName())
			notificationInput = input
			break
		}
	}

	// avoid recursive replication
	if config.GetBool("notification.source.filer.enabled") && config.GetBool("notification.sink.filer.enabled") {
		sourceConfig, sinkConfig := config.Sub("source.filer"), config.Sub("sink.filer")
		if sourceConfig.GetString("grpcAddress") == sinkConfig.GetString("grpcAddress") {
			fromDir := sourceConfig.GetString("directory")
			toDir := sinkConfig.GetString("directory")
			if strings.HasPrefix(toDir, fromDir) {
				glog.Fatalf("recursive replication! source directory %s includes the sink directory %s", fromDir, toDir)
			}
		}
	}

	var dataSink sink.ReplicationSink
	for _, sk := range sink.Sinks {
		if config.GetBool("sink." + sk.GetName() + ".enabled") {
			viperSub := config.Sub("sink." + sk.GetName())
			if err := sk.Initialize(viperSub); err != nil {
				glog.Fatalf("Failed to initialize sink for %s: %+v",
					sk.GetName(), err)
			}
			glog.V(0).Infof("Configure sink to %s", sk.GetName())
			dataSink = sk
			break
		}
	}

	if dataSink == nil {
		println("no data sink configured:")
		for _, sk := range sink.Sinks {
			println("    " + sk.GetName())
		}
		return true
	}

	replicator := replication.NewReplicator(config.Sub("source.filer"), dataSink)

	for {
		key, m, err := notificationInput.ReceiveMessage()
		if err != nil {
			glog.Errorf("receive %s: %+v", key, err)
			continue
		}
		if m.OldEntry != nil && m.NewEntry == nil {
			glog.V(1).Infof("delete: %s", key)
		} else if m.OldEntry == nil && m.NewEntry != nil {
			glog.V(1).Infof("   add: %s", key)
		} else {
			glog.V(1).Infof("modify: %s", key)
		}
		if err = replicator.Replicate(key, m); err != nil {
			glog.Errorf("replicate %s: %+v", key, err)
		}
	}

	return true
}