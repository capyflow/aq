package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "aq",
	Short: "Aq is a tool for processing various types of data.",
	Long:  "Aq is a tool for processing various types of data. It can be used to process data from different sources, such as files, databases, and APIs.",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of Aq",
	Long:  `All software has versions. This is Aq's`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Aq v0.1 -- HEAD")
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(tomlCmd)
}
