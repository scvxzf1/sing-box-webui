//go:build linux

package privilege

import "os"

func IsElevated() bool {
	return os.Geteuid() == 0
}
