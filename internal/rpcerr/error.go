// Package rpcerr provides cycle-free errors shared by leaf packages and RPC.
package rpcerr

import "fmt"

type CodedError struct{ Code, Msg string }

func (e *CodedError) Error() string { return e.Msg }
func Errf(code, msg string, a ...any) *CodedError {
	return &CodedError{Code: code, Msg: fmt.Sprintf(msg, a...)}
}
