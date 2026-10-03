package parsers

import (
	"context"
	"io"

	"talkaboutthis/internal/domain"
)

// TXTParser extrae texto plano de archivos .txt.
//
// Es el parser de referencia: define el contrato minimo que cumplen los demas.
type TXTParser struct{}

// NewTXTParser construye el parser de texto plano.
func NewTXTParser() *TXTParser { return &TXTParser{} }

// CanParse implementa domain.DocumentParser.
//
// La comparacion es case-insensitive porque un archivo "NOTAS.TXT" es el mismo
// formato que "notas.txt"; fallar aqui seria una sorpresa inutil para el
// usuario.
func (p *TXTParser) CanParse(extension string) bool {
	return normalizarExtension(extension) == domain.ExtensionTexto
}

// Parse implementa domain.DocumentParser.
//
// Para texto plano, "parsear" es leer y limpiar. No se hace mas: un .txt puede
// contener cualquier cosa y asumir una estructura seria convertir el parser en
// un interprete de minutas.
func (p *TXTParser) Parse(ctx context.Context, reader io.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	return leerTexto(reader)
}

// Asercion de compilacion: si la firma de DocumentParser cambia, el paquete
// deja de compilar y el error es explicito en lugar de fallar en ejecucion.
var _ domain.DocumentParser = (*TXTParser)(nil)
