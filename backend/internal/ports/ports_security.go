package ports

// --- Seguridad (puertos de salida hacia crypto/tokens) ---

type PasswordHasher interface {
	Hash(plain string) (string, error)
	Compare(hash, plain string) bool
	// DummyCompare gasta el tiempo de un Compare real y siempre devuelve false.
	// Lo usa el login cuando el usuario no existe: sin eso, el tiempo de
	// respuesta delata qué emails están registrados.
	DummyCompare(plain string) bool
}

type TokenIssuer interface {
	Issue(userID string) (string, error)
	Parse(token string) (string, error) // devuelve userID
}
