package httpkit

import (
	"errors"
)

// ErrFakeInfra simula un fallo de infraestructura del repositorio.
var ErrFakeInfra = errors.New(`pq: password authentication failed for user "salon"`)
