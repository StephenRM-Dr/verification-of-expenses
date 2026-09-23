package utils

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// LimpiarTexto normaliza el texto y capitaliza la primera letra.
func LimpiarTexto(s string) string {
	words := strings.Fields(s)
	res := strings.Join(words, " ")
	if len(res) == 0 {
		return ""
	}
	runes := []rune(strings.ToLower(res))
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// LeerCadena lee una línea y ofrece un valor por defecto si está vacío.
func LeerCadena(scanner *bufio.Scanner, label string, actual string) string {
	if actual != "" {
		fmt.Printf("%s [%s]: ", label, actual)
	} else {
		fmt.Printf("%s: ", label)
	}
	scanner.Scan()
	input := strings.TrimSpace(scanner.Text())
	if input == "" && actual != "" {
		return actual
	}
	return input
}

// LeerFlotante asegura que la entrada sea un número válido.
func LeerFlotante(scanner *bufio.Scanner, label string, actual float64) float64 {
	for {
		input := LeerCadena(scanner, label, fmt.Sprintf("%.2f", actual))
		val, err := strconv.ParseFloat(input, 64)
		if err == nil {
			return val
		}
		fmt.Println("❌ Error: Ingrese un número válido (ej: 1250.50)")
	}
}

// Pausa detiene la ejecución para que el usuario lea los mensajes de éxito/error.
func Pausa(scanner *bufio.Scanner) {
	fmt.Print("\nPresione ENTER para continuar...")
	scanner.Scan()
}
