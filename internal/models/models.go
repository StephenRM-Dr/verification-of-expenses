package models

// Transaccion representa la estructura de los datos de un movimiento financiero.
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
