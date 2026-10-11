package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/dexidp/dex/storage"
)

// newErrorCodeInterceptor gives the management API's errors a gRPC code. The
// handlers in server/apiserver return plain errors, which reach the caller as
// codes.Unknown, so a bad request, a missing object and a broken database all
// look the same. The mapping lives here rather than in the handlers because
// those files are upstream's and change on every sync; the message is kept
// verbatim, so callers that match on it keep working.
func newErrorCodeInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}
		if s, ok := status.FromError(err); ok && s.Code() != codes.Unknown {
			return resp, err
		}
		return resp, status.Error(grpcCode(err), err.Error())
	}
}

// grpcCode classifies an error from server/apiserver. The handlers build most
// of them with errors.New and fmt.Errorf("...: %v"), which drops the storage
// sentinel, so after the typed checks the message is all there is.
// TestEveryAPIServerMessageIsClassified walks the handlers' source and fails
// when a validation message would fall through to Internal, so a new one from
// upstream shows up at merge time instead of as a server error.
func grpcCode(err error) codes.Code {
	var b64 base64.CorruptInputError
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return codes.NotFound
	case errors.Is(err, storage.ErrAlreadyExists):
		return codes.AlreadyExists
	case errors.As(err, &b64), errors.Is(err, proto.Error):
		// A subject that is not base64 protobuf: the caller sent garbage.
		return codes.InvalidArgument
	}

	msg := err.Error()
	switch {
	case strings.HasSuffix(msg, ": "+storage.ErrNotFound.Error()):
		return codes.NotFound
	case strings.Contains(msg, "feature flag is not enabled"):
		return codes.Unimplemented
	case strings.Contains(msg, "read-only"),
		strings.Contains(msg, "comes from dex's configuration file"),
		strings.Contains(msg, "is not configured"):
		return codes.FailedPrecondition
	case strings.HasSuffix(msg, " supplied"),
		msg == "nothing to update",
		strings.HasPrefix(msg, "unknown grant type"),
		strings.Contains(msg, "must not be empty"),
		strings.HasPrefix(msg, "invalid refresh token lifetime"),
		strings.HasPrefix(msg, "parsing bcrypt hash"),
		strings.HasPrefix(msg, "given hash cost"):
		return codes.InvalidArgument
	}
	return codes.Internal
}
