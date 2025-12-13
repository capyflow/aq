package cmd

import (
	"fmt"

	"github.com/dzjyyds666/aq/pkg"
	"github.com/spf13/cobra"
)

type TomlParams struct {
	Find   string `json:"find"`   // 查找的key
	Input  string `json:"input"`  // 输入文件路径
	Output string `json:"output"` // 输出文件地址
}

var params *TomlParams

var inputStruct any // 解析到的toml之后存放在这个结构体中

var tomlCmd = &cobra.Command{
	Use:   "toml",
	Short: "toml parse tools",
	Run:   tomlRun,
}

func init() {
	params = &TomlParams{}
	tomlCmd.Flags().StringVarP(&params.Find, "find", "f", "", "find")
	tomlCmd.Flags().StringVarP(&params.Input, "input", "i", "", "input file path")
	tomlCmd.Flags().StringVarP(&params.Output, "output", "o", "", "output path")
}

func tomlRun(cmd *cobra.Command, args []string) {
	if len(params.Input) == 0 {
		fmt.Println("no input file path")
		return
	}
	exist, err := pkg.CheckFileExist(params.Input)
	if err != nil {
		fmt.Println("check file exist error:", err)
		return
	}
	if !exist {
		fmt.Println("input file not exist")
		return
	}

}
