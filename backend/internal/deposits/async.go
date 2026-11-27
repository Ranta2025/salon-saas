package deposits

import (
	"context"

	"salon-saas/internal/kernel"
)

// AsyncRunner y runAsync viven en el núcleo (kernel): los comparten la
// recuperación de cuenta, la verificación de email y el aviso de la seña.
type AsyncRunner = kernel.AsyncRunner

func runAsync(run AsyncRunner, job func(ctx context.Context)) { kernel.RunAsync(run, job) }
