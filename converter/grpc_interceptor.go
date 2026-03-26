package converter

import (
	"context"
	"fmt"
	"slices"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	commonpb "go.temporal.io/api/common/v1"
	failurepb "go.temporal.io/api/failure/v1"
	"go.temporal.io/api/proxy"
)

var payloadErrorTypes = []string{"temporal.api.errordetails.v1.QueryFailedFailure", "temporal.api.errordetails.v1.MultiOperationExecutionFailure"}

func visitGrpcErrorPayload(ctx context.Context, s *status.Status, inbound proxy.VisitPayloadsOptions) error {
	p := s.Proto()
	for _, detail := range p.Details {
		if slices.Contains(payloadErrorTypes, string(detail.MessageName())) {
			if vErr := proxy.VisitPayloads(ctx, detail, inbound); vErr != nil {
				return vErr
			}
		}
	}
	return status.ErrorProto(p)
}

func visitGrpcErrorFailure(ctx context.Context, s *status.Status, inbound proxy.VisitFailuresOptions) error {
	p := s.Proto()
	for _, detail := range p.Details {
		if slices.Contains(payloadErrorTypes, string(detail.MessageName())) {
			if vErr := proxy.VisitFailures(ctx, detail, inbound); vErr != nil {
				return vErr
			}
		}
	}
	return status.ErrorProto(p)
}

// PayloadCodecGRPCClientInterceptorOptions holds interceptor options.
type PayloadCodecGRPCClientInterceptorOptions struct {
	Codecs      []PayloadCodec
	// Concurrency sets the maximum number of concurrent payload encoding/decodings.
	// If 0 or 1, single-threaded execution is used.
	Concurrency int
}

// NewPayloadCodecGRPCClientInterceptor returns a GRPC Client Interceptor that will mimic the encoding
// that the SDK system would perform when configured with a matching EncodingDataConverter.
// When combining this with NewFailureGRPCClientInterceptor you should ensure that NewFailureGRPCClientInterceptor is
// before NewPayloadCodecGRPCClientInterceptor in the chain.
//
// Note: This approach does not support use cases that rely on the ContextAware DataConverter interface as
// workflow context is not available at the GRPC level.
func NewPayloadCodecGRPCClientInterceptor(options PayloadCodecGRPCClientInterceptorOptions) (grpc.UnaryClientInterceptor, error) {
	concurrency := options.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	return func(ctx context.Context, method string, req, response interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var eg *errgroup.Group
		eg = new(errgroup.Group)
		eg.SetLimit(concurrency)

		outbound := proxy.VisitPayloadsOptions{
			Visitor: func(vpc *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
				if concurrency == 1 {
					var err error
					for i := len(options.Codecs) - 1; i >= 0; i-- {
						if payloads, err = options.Codecs[i].Encode(payloads); err != nil {
							return payloads, err
						}
					}
					return payloads, nil
				}

				originalPayloads := make([]*commonpb.Payload, len(payloads))
				copy(originalPayloads, payloads)

				eg.Go(func() error {
					var err error
					encoded := originalPayloads
					for i := len(options.Codecs) - 1; i >= 0; i-- {
						if encoded, err = options.Codecs[i].Encode(encoded); err != nil {
							return err
						}
					}
					for i, p := range originalPayloads {
						*p = *encoded[i]
					}
					return nil
				})
				return payloads, nil
			},
			SkipSearchAttributes: true,
		}

		outbound.WellKnownAnyVisitor = func(vpc *proxy.VisitPayloadsContext, p *anypb.Any) error {
			child, err := p.UnmarshalNew()
			if err != nil {
				return fmt.Errorf("failed to unmarshal any: %w", err)
			}
			if err := proxy.VisitPayloads(ctx, child, outbound); err != nil {
				return err
			}
			if err := eg.Wait(); err != nil {
				return err
			}
			eg = new(errgroup.Group)
			eg.SetLimit(concurrency)

			if err := p.MarshalFrom(child); err != nil {
				return fmt.Errorf("failed to marshal any: %w", err)
			}
			return nil
		}

		if reqMsg, ok := req.(proto.Message); ok {
			if err := proxy.VisitPayloads(ctx, reqMsg, outbound); err != nil {
				return err
			}
			if err := eg.Wait(); err != nil {
				return err
			}
		}

		err := invoker(ctx, method, req, response, cc, opts...)

		var inEg *errgroup.Group
		inEg = new(errgroup.Group)
		inEg.SetLimit(concurrency)

		inbound := proxy.VisitPayloadsOptions{
			Visitor: func(vpc *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
				if concurrency == 1 {
					var err error
					for _, codec := range options.Codecs {
						if payloads, err = codec.Decode(payloads); err != nil {
							return payloads, err
						}
					}
					return payloads, nil
				}

				originalPayloads := make([]*commonpb.Payload, len(payloads))
				copy(originalPayloads, payloads)

				inEg.Go(func() error {
					var err error
					decoded := originalPayloads
					for _, codec := range options.Codecs {
						if decoded, err = codec.Decode(decoded); err != nil {
							return err
						}
					}
					for i, p := range originalPayloads {
						*p = *decoded[i]
					}
					return nil
				})
				return payloads, nil
			},
			SkipSearchAttributes: true,
		}

		inbound.WellKnownAnyVisitor = func(vpc *proxy.VisitPayloadsContext, p *anypb.Any) error {
			child, err := p.UnmarshalNew()
			if err != nil {
				return fmt.Errorf("failed to unmarshal any: %w", err)
			}
			if err := proxy.VisitPayloads(ctx, child, inbound); err != nil {
				return err
			}
			if err := inEg.Wait(); err != nil {
				return err
			}
			inEg = new(errgroup.Group)
			inEg.SetLimit(concurrency)

			if err := p.MarshalFrom(child); err != nil {
				return fmt.Errorf("failed to marshal any: %w", err)
			}
			return nil
		}

		if err != nil {
			if s, ok := status.FromError(err); ok {
				err = visitGrpcErrorPayload(ctx, s, inbound)
				if waitErr := inEg.Wait(); waitErr != nil {
					err = waitErr
				}
				inEg = new(errgroup.Group)
				inEg.SetLimit(concurrency)
			}
		}

		if resMsg, ok := response.(proto.Message); ok {
			if visitErr := proxy.VisitPayloads(ctx, resMsg, inbound); visitErr != nil {
				err = visitErr
			} else if waitErr := inEg.Wait(); waitErr != nil {
				err = waitErr
			}
		}

		return err
	}, nil
}

// NewFailureGRPCClientInterceptorOptions holds interceptor options.
type NewFailureGRPCClientInterceptorOptions struct {
	// DataConverter is optional. If not set the SDK's dataconverter will be used.
	DataConverter DataConverter
	// Whether to Encode attributes. The current implementation requires this be true.
	EncodeCommonAttributes bool
	// Concurrency sets the maximum number of concurrent failure encodings/decodings.
	// If 0 or 1, single-threaded execution is used.
	Concurrency int
}

// NewFailureGRPCClientInterceptor returns a GRPC Client Interceptor that will mimic the encoding
// that the SDK system would perform when configured with a FailureConverter with the EncodeCommonAttributes option set.
// When combining this with NewPayloadCodecGRPCClientInterceptor you should ensure that NewFailureGRPCClientInterceptor is
// before NewPayloadCodecGRPCClientInterceptor in the chain.
func NewFailureGRPCClientInterceptor(options NewFailureGRPCClientInterceptorOptions) (grpc.UnaryClientInterceptor, error) {
	if !options.EncodeCommonAttributes {
		return nil, fmt.Errorf("EncodeCommonAttributes must be set for this interceptor to function")
	}

	dc := options.DataConverter
	if dc == nil {
		dc = GetDefaultDataConverter()
	}

	concurrency := options.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	return func(ctx context.Context, method string, req, response interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var eg *errgroup.Group
		eg = new(errgroup.Group)
		eg.SetLimit(concurrency)

		outbound := proxy.VisitFailuresOptions{
			Visitor: func(vpc *proxy.VisitFailuresContext, failure *failurepb.Failure) error {
				if concurrency == 1 {
					return EncodeCommonFailureAttributes(dc, failure)
				}
				eg.Go(func() error {
					return EncodeCommonFailureAttributes(dc, failure)
				})
				return nil
			},
		}

		outbound.WellKnownAnyVisitor = func(vpc *proxy.VisitFailuresContext, p *anypb.Any) error {
			child, err := p.UnmarshalNew()
			if err != nil {
				return fmt.Errorf("failed to unmarshal any: %w", err)
			}
			if err := proxy.VisitFailures(ctx, child, outbound); err != nil {
				return err
			}
			if err := eg.Wait(); err != nil {
				return err
			}
			eg = new(errgroup.Group)
			eg.SetLimit(concurrency)

			if err := p.MarshalFrom(child); err != nil {
				return fmt.Errorf("failed to marshal any: %w", err)
			}
			return nil
		}

		if reqMsg, ok := req.(proto.Message); ok {
			if err := proxy.VisitFailures(ctx, reqMsg, outbound); err != nil {
				return err
			}
			if err := eg.Wait(); err != nil {
				return err
			}
		}

		err := invoker(ctx, method, req, response, cc, opts...)

		var inEg *errgroup.Group
		inEg = new(errgroup.Group)
		inEg.SetLimit(concurrency)

		inbound := proxy.VisitFailuresOptions{
			Visitor: func(vpc *proxy.VisitFailuresContext, failure *failurepb.Failure) error {
				if concurrency == 1 {
					DecodeCommonFailureAttributes(dc, failure)
					return nil
				}
				inEg.Go(func() error {
					DecodeCommonFailureAttributes(dc, failure)
					return nil
				})
				return nil
			},
		}

		inbound.WellKnownAnyVisitor = func(vpc *proxy.VisitFailuresContext, p *anypb.Any) error {
			child, err := p.UnmarshalNew()
			if err != nil {
				return fmt.Errorf("failed to unmarshal any: %w", err)
			}
			if err := proxy.VisitFailures(ctx, child, inbound); err != nil {
				return err
			}
			if err := inEg.Wait(); err != nil {
				return err
			}
			inEg = new(errgroup.Group)
			inEg.SetLimit(concurrency)

			if err := p.MarshalFrom(child); err != nil {
				return fmt.Errorf("failed to marshal any: %w", err)
			}
			return nil
		}

		if err != nil {
			if s, ok := status.FromError(err); ok {
				err = visitGrpcErrorFailure(ctx, s, inbound)
				if waitErr := inEg.Wait(); waitErr != nil {
					err = waitErr
				}
				inEg = new(errgroup.Group)
				inEg.SetLimit(concurrency)
			}
		}

		if resMsg, ok := response.(proto.Message); ok {
			if visitErr := proxy.VisitFailures(ctx, resMsg, inbound); visitErr != nil {
				err = visitErr
			} else if waitErr := inEg.Wait(); waitErr != nil {
				err = waitErr
			}
		}

		return err
	}, nil
}
