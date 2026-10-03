// Paquete logging configura la observabilidad de la aplicacion (RNF-06).
//
// Usa log/slog de la biblioteca estandar: ninguna dependencia externa.
//
// # IDENTIFICADORES DE CORRELACION
//
// Cada ejecucion recibe un Job ID que acompana TODOS sus logs. Sin el, un
// proceso que extrae veinte item produce lineas indistinguibles y es imposible
// reconstruir que paso con un trabajo concreto.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

// claveContexto es la clave privada con la que se guarda el Job ID en un
// context. El tipo no exportado evita colisiones con otras claves del proyecto.
type claveContexto struct{}

// Formato de salida del log.
type Formato string

const (
	// FormatoJSON produce una linea JSON por evento: lo que esperan los
	// agregadores de logs y los sistemas de CI.
	FormatoJSON Formato = "json"

	// FormatoTexto produce lineas legibles: mejor para uso interactivo.
	FormatoTexto Formato = "text"
)

// FormatosValidos enumera los formatos aceptados.
var FormatosValidos = []Formato{FormatoJSON, FormatoTexto}

// ParseFormato valida un formato de salida.
//
// La comparacion es insensible a mayusculas porque el valor suele venir de un
// flag escrito por una persona.
func ParseFormato(valor string) (Formato, error) {
	normalizado := Formato(strings.ToLower(strings.TrimSpace(valor)))

	for _, valido := range FormatosValidos {
		if normalizado == valido {
			return normalizado, nil
		}
	}

	return "", fmt.Errorf("%w: formato de log desconocido %q (usa json o text)", ErrFormato, valor)
}

// Configuracion del logger.
type Configuracion struct {
	// Nivel: debug, info, warn o error.
	Nivel string
	// Formato: json o text.
	Formato Formato
}

// ErrFormato permite comparar los errores de configuracion del logger con
// errors.Is, sin depender del texto del mensaje.
var ErrFormato = errors.New("configuracion de log invalida")

// Nuevo construye el logger.
//
// El writer se inyecta para que los tests puedan capturar la salida, en lugar de
// escribir siempre en stderr.
func Nuevo(cfg Configuracion, destino io.Writer) (*slog.Logger, error) {
	nivel, err := parseNivel(cfg.Nivel)
	if err != nil {
		return nil, err
	}

	formato := cfg.Formato
	if formato == "" {
		formato = FormatoTexto
	}

	opts := &slog.HandlerOptions{Level: nivel}

	var handler slog.Handler

	switch formato {
	case FormatoJSON:
		handler = slog.NewJSONHandler(destino, opts)
	case FormatoTexto:
		handler = slog.NewTextHandler(destino, opts)
	default:
		return nil, fmt.Errorf("%w: formato desconocido: %q", ErrFormato, formato)
	}

	return slog.New(handler), nil
}

// parseNivel traduce el nombre del nivel a un slog.Level.
func parseNivel(nombre string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(nombre)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("%w: nivel de log desconocido: %q", ErrFormato, nombre)
	}
}

// NuevoJobID genera un identificador de correlacion.
//
// Se usan bytes aleatorios, no una marca de tiempo: dos ejecuciones en el mismo
// segundo deben poder distinguirse, que es justo lo que un proceso de CI
// paralelo hace constantemente.
func NuevoJobID() string {
	buffer := make([]byte, 6)

	if _, err := rand.Read(buffer); err != nil {
		// crypto/rand no falla en practica. Si fallara, se degrada a una
		// marca de tiempo: es peor que nada, pero no motivo para abortar la
		// ejecucion del usuario por un identificador de log.
		return fmt.Sprintf("job-%d", time.Now().UnixNano())
	}

	return "job-" + hex.EncodeToString(buffer)
}

// ConJobID devuelve un logger que incluye el Job ID en cada evento.
func ConJobID(base *slog.Logger, jobID string) *slog.Logger {
	if base == nil || jobID == "" {
		return base
	}

	return base.With(slog.String("job_id", jobID))
}

// ConJobIDEnContexto guarda el Job ID en un context para propagarlo.
func ConJobIDEnContexto(ctx context.Context, jobID string) context.Context {
	return context.WithValue(ctx, claveContexto{}, jobID)
}

// JobIDDeContexto recupera el Job ID de un context.
func JobIDDeContexto(ctx context.Context) string {
	if ctx == nil {
		return ""
	}

	jobID, ok := ctx.Value(claveContexto{}).(string)
	if !ok {
		return ""
	}

	return jobID
}
