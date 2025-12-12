package pkg

import "os"

// CheckFileExist 检查文件是否存在
func CheckFileExist(filePath string) (bool, error) {
	_, err := os.Lstat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
