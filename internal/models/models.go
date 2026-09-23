package models

// Transaccion represents the data structure of a financial transaction.
type Transaccion struct {
	ID          int     `json:"id"`
	FechaPago   string  `json:"fecha_pago"`
	Descripcion string  `json:"descripcion"`
	Monto       float64 `json:"monto"`
	Ciudad      string  `json:"ciudad"`
	Banco       string  `json:"banco_usado"`
	Referencia  string  `json:"referencia"`
	ImagenPath  string  `json:"imagen_path"`
}
