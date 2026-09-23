package export

import (
	"strconv"

	"example.com/m/v2/internal/models"
	"github.com/xuri/excelize/v2"
)

// GenerateExcel genera un objeto excelize.File con las transacciones.
func GenerateExcel(transacciones []models.Transaccion) (*excelize.File, error) {
	f := excelize.NewFile()
	
	sheetName := "Historial"
	index, err := f.NewSheet(sheetName)
	if err != nil {
		return nil, err
	}

	// Eliminar la hoja por defecto (Sheet1)
	f.DeleteSheet("Sheet1")

	// Encabezados
	headers := []string{"ID", "Fecha", "Descripción", "Monto", "Ciudad", "Banco", "Referencia"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheetName, cell, h)
	}

	// Estilo para el encabezado
	style, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#D3D3D3"}, Pattern: 1},
	})
	if err == nil {
		f.SetCellStyle(sheetName, "A1", "G1", style)
	}

	// Datos
	for rowIdx, t := range transacciones {
		row := rowIdx + 2
		f.SetCellValue(sheetName, "A"+strconv.Itoa(row), t.ID)
		f.SetCellValue(sheetName, "B"+strconv.Itoa(row), t.FechaPago)
		f.SetCellValue(sheetName, "C"+strconv.Itoa(row), t.Descripcion)
		f.SetCellValue(sheetName, "D"+strconv.Itoa(row), t.Monto)
		f.SetCellValue(sheetName, "E"+strconv.Itoa(row), t.Ciudad)
		f.SetCellValue(sheetName, "F"+strconv.Itoa(row), t.Banco)
		f.SetCellValue(sheetName, "G"+strconv.Itoa(row), t.Referencia)
	}

	f.SetColWidth(sheetName, "A", "A", 10)
	f.SetColWidth(sheetName, "B", "B", 15)
	f.SetColWidth(sheetName, "C", "C", 35)
	f.SetColWidth(sheetName, "D", "D", 15)
	f.SetColWidth(sheetName, "E", "G", 20)

	f.SetActiveSheet(index)
	return f, nil
}

// ExportToExcel genera un archivo Excel físico (usado por la CLI).
func ExportToExcel(transacciones []models.Transaccion, fileName string) error {
	f, err := GenerateExcel(transacciones)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := f.SaveAs(fileName); err != nil {
		return err
	}

	return nil
}
