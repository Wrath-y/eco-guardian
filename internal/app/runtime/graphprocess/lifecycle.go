package graphprocess

import "context"

// Lifecycle adapts selection/startup and the supervisor to the composition
// root's dependency worker seam. Its Close is therefore ordered after HTTP
// admission, durable safe points, and normal worker shutdown.
type Lifecycle struct {
	StartFunction func(context.Context) error
	Supervisor    *Supervisor
}

func (lifecycle Lifecycle) Start(ctx context.Context) error {
	if lifecycle.StartFunction == nil || lifecycle.Supervisor == nil {
		return ErrSupervisorInvalid
	}
	return lifecycle.StartFunction(ctx)
}

func (lifecycle Lifecycle) Close(ctx context.Context) error {
	if lifecycle.Supervisor == nil {
		return ErrSupervisorInvalid
	}
	return lifecycle.Supervisor.Stop(ctx)
}
