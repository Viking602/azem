package agentruntime

import "context"

type Projector interface {
	Project(context.Context, []Event) (Projection, error)
}

type UserTimelineProjector interface {
	ProjectUserTimeline(context.Context, []Event) ([]RunTimelineItem, error)
}
