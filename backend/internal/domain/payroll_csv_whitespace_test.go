package domain

import "testing"

// D8 endurecido (F3.3): la neutralización de fórmulas no mira solo el PRIMER
// carácter. Un programa de planillas interpreta como fórmula el primer carácter
// NO blanco de la celda, así que " =SUM(A1)" también se ejecuta. Las reglas:
//   - tab o CR como primer carácter: se neutralizan igual que antes;
//   - si no, se saltan los espacios Unicode iniciales (espacio, NBSP U+00A0,
//     entre otros) y se neutraliza cuando el primero que queda es = + - @ o su
//     forma de ancho completo (＝ ＋ － ＠);
//   - los números y los nombres comunes quedan intactos (PRESERVE).
func TestCSVCell_neutraliza_formulas_con_espacios_y_ancho_completo(t *testing.T) {
	cases := []struct{ name, in, want string }{
		// Lo de antes sigue igual (PRESERVE de la regla original).
		{"igual", "=SUM(A1:A9)", "'=SUM(A1:A9)"},
		{"mas", "+1+1", "'+1+1"},
		{"menos", "-2", "'-2"},
		{"arroba", "@cmd", "'@cmd"},
		{"tab", "\tAna", "'\tAna"},
		{"cr", "\rAna", "\"'\rAna\""},

		// Espacio ASCII: el primer carácter NO blanco decide.
		{"espacio igual", " =SUM(A1)", "' =SUM(A1)"},
		{"dos espacios mas", "  +1", "'  +1"},
		{"espacio menos", " -2", "' -2"},
		{"espacio arroba", " @cmd", "' @cmd"},

		// Espacios Unicode: NBSP (U+00A0), em space (U+2003), ideográfico
		// (U+3000) y espacio fino (U+2009).
		{"nbsp igual", "\u00a0=SUM(A1)", "'\u00a0=SUM(A1)"},
		{"em space mas", "\u2003+1", "'\u2003+1"},
		{"ideografico menos", "\u3000-2", "'\u3000-2"},
		{"fino arroba", "\u2009@cmd", "'\u2009@cmd"},
		{"mezcla de espacios", " \u00a0\t =SUM(A1)", "' \u00a0\t =SUM(A1)"},

		// Ancho completo (formularios CJK).
		{"fullwidth igual", "＝SUM(A1)", "'＝SUM(A1)"},
		{"fullwidth mas", "＋1", "'＋1"},
		{"fullwidth menos", "－2", "'－2"},
		{"fullwidth arroba", "＠cmd", "'＠cmd"},
		{"fullwidth con espacio", "\u00a0＝SUM(A1)", "'\u00a0＝SUM(A1)"},

		// PRESERVE: números y nombres comunes no se tocan.
		{"numero", "123", "123"},
		{"decimal", "3.50", "3.50"},
		{"nombre", "Ana", "Ana"},
		{"nombre unicode", "Ñandú Pérez", "Ñandú Pérez"},
		{"nombre con espacios", "  Ana  ", "  Ana  "},
		{"vacio", "", ""},
		// Una celda solo de blancos no tiene fórmula que neutralizar.
		{"solo espacios", "   ", "   "},
		// El signo después del texto no es fórmula.
		{"texto con igual adentro", "Ana=1", "Ana=1"},
		// Formas que NO son ASCII ni ancho completo.
		{"guion largo", "–2", "–2"},
	}
	for _, c := range cases {
		if got := CSVCell(c.in); got != c.want {
			t.Fatalf("%s: CSVCell(%q) = %q, esperaba %q", c.name, c.in, got, c.want)
		}
	}
}
