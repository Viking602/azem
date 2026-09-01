package agentruntime

import "context"

type OutputGateway interface {
	Publish(context.Context, UserMessage) error
}
