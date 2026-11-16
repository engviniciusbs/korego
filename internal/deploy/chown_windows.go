//go:build windows

package deploy

import "os"

func chownLike(string, os.FileInfo) {}
