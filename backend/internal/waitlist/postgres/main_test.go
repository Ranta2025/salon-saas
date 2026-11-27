// TestMain deja la base de integración migrada antes de cualquier test.
package postgres

import (
	"os"
	"testing"

	"salon-saas/internal/testutil/itdb"
)

func TestMain(m *testing.M) { os.Exit(itdb.Run(m)) }
