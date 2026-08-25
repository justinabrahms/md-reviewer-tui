package ui

import "os"

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}

func statFile(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
