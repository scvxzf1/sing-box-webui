package core

import _ "embed"

const (
	embeddedVersion = "1.13.16"
	embeddedDigest  = "e37c312859dfa84cba148f41072ff6369f08361ae91d622dc1fd3aab49611a8d"
)

//go:embed assets/sing-box-1.13.16-linux-amd64.tar.gz
var embeddedArchive []byte
