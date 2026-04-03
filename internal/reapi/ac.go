package reapi

import (
	"context"
	"fmt"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetActionResult looks up an ActionResult by action digest.
// Returns (nil, nil) on cache miss (NOT_FOUND).
func (c *Client) GetActionResult(ctx context.Context, actionDigest Digest) (*repb.ActionResult, error) {
	resp, err := retryRPCWithCtx(ctx, c.cb, func() (*repb.ActionResult, error) {
		rCtx, cancel := c.rpcCtx(ctx)
		defer cancel()

		acClient := c.acClient
		resp, err := acClient.GetActionResult(rCtx, &repb.GetActionResultRequest{
			InstanceName: c.instanceName,
			ActionDigest: actionDigest.ToProto(),
		})
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return nil, nil
			}
			return nil, fmt.Errorf("GetActionResult: %w", err)
		}
		return resp, nil
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// UpdateActionResult stores an ActionResult keyed by action digest.
// The returned error preserves the original gRPC status code so callers
// can inspect it with status.Code().
func (c *Client) UpdateActionResult(ctx context.Context, actionDigest Digest, result *repb.ActionResult) error {
	return retryRPCNoResultWithCtx(ctx, c.cb, func() error {
		rCtx, cancel := c.rpcCtx(ctx)
		defer cancel()

		acClient := c.acClient
		_, err := acClient.UpdateActionResult(rCtx, &repb.UpdateActionResultRequest{
			InstanceName: c.instanceName,
			ActionDigest: actionDigest.ToProto(),
			ActionResult: result,
		})
		return err
	})
}
