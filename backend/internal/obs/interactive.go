package obs

import "context"

type interactiveKey struct{}

// Interactive marks ctx so OBS requests made with it bypass the background
// rate limiter. Used for user-facing API requests, which must not queue
// behind background polling.
func Interactive(ctx context.Context) context.Context {
	return context.WithValue(ctx, interactiveKey{}, true)
}

func isInteractive(ctx context.Context) bool {
	v, _ := ctx.Value(interactiveKey{}).(bool)
	return v
}
