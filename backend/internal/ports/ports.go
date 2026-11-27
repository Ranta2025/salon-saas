// Package ports declara lo que el dominio NECESITA, sin decir cómo se hace.
// Los adaptadores (postgres, bcrypt, jwt, http) implementan estas interfaces.
// Esto es DIP: app depende de abstracciones, no de Gin ni SQL.
//
// El paquete está partido por FUNCIONALIDAD, un archivo por módulo
// (`ports_<feature>.go`): el código de una funcionalidad y sus puertos se leen
// juntos. Es el mismo paquete, así que no cambia ni una firma.
package ports
