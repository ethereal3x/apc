package storage

import "errors"

var (
	ErrBucketNotFound = errors.New("storage: bucket not found")
	ErrInvalidConfig  = errors.New("storage: invalid configuration")
	ErrInvalidObject  = errors.New("storage: invalid object")
	ErrUploadCanceled = errors.New("storage: upload canceled")
)
