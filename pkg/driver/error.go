package driver

import "errors"

var (
	errNilProtocol       = errors.New("protocol is nil")
	errs3ProtocolMissing = errors.New("S3 protocol not defined")
)
