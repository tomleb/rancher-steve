package otel

import (
	"go.opentelemetry.io/otel"
)

const name = "github.com/rancher/steve/pkg/otel"

var (
	Tracer = otel.Tracer(name)
)
