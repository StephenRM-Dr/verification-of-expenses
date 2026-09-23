package utils

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// LimpiarTexto normalizes the text and capitalizes the first letter.
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

// LeerCadena reads a line and offers a default value if it is empty.
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

// LeerFlotante ensures the input is a valid number.
func LeerFlotante(scanner *bufio.Scanner, label string, actual float64) float64 {
	for {
		input := LeerCadena(scanner, label, fmt.Sprintf("%.2f", actual))
		val, err := strconv.ParseFloat(input, 64)
		if err == nil {
			return val
		}
		fmt.Println("❌ Error: Enter a valid number (e.g. 1250.50)")
	}
}

// Pausa pauses execution so the user can read the success/error messages.
func Pausa(scanner *bufio.Scanner) {
	fmt.Print("\nPress ENTER to continue...")
	scanner.Scan()
}
