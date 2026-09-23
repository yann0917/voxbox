package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "voxbox",
		Short:   "语音 AI 工具箱",
		Version: version,
	}
	root.AddCommand(
		newConfigCmd(),
		newDictCmd(),
		newVoicesCmd(),
		newTTSCommand(),
		newTTSLongCommand(),
		newTTSStreamCommand(),
		newASRCommand(),
		newPodcastCommand(),
		newSeparateCommand(),
		newAudioCommand(),
		newTranslateCommand(),
		newMinutesCommand(),
		newServeCommand(),
		newMCPCmd(),
		newSkillCmd(),
	)
	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(2)
	}
}
