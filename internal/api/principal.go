package api

import (
	"context"

	"github.com/vquie/MailTail/internal/models"
)

type principalContextKey struct{}

func withPrincipal(ctx context.Context, principal models.SessionPrincipal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func currentPrincipal(ctx context.Context) models.SessionPrincipal {
	principal, ok := ctx.Value(principalContextKey{}).(models.SessionPrincipal)
	if !ok {
		return models.SessionPrincipal{IsAdmin: true, Username: "anonymous"}
	}
	return principal
}
