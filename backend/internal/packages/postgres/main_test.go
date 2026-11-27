// TestMain deja la base de integración migrada antes de cualquier test del
// paquete: así el orden de archivos no importa y una DB recién creada (scratch)
// sirve igual que la de desarrollo.
package postgres

import (
	"os"
	"testing"

	"salon-saas/internal/testutil/itdb"
)

func TestMain(m *testing.M) { os.Exit(itdb.Run(m)) }
