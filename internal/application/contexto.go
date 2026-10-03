package application

import (
	"context"
	"log/slog"
)

// claveContextoLogger es la clave con la que se guarda el logger en un context.
//
// El tipo no exportado evita colisiones con otras claves. El logger vive aqui, y
// no en el paquete infrastructure/logging, porque la capa de aplicacion no puede
// importar infraestructura: el accessor del contexto es una necesidad de esta
// capa, no de aquella.
type claveContextoLogger struct{}

// ConLoggerEnContexto guarda un logger para que las etapas siguientes lo recuperen.
//
// Esta exportada para que la CLI pueda inyectar SU logger (con el job_id puesto
// y el formato elegido) en lugar de dejar que cada capa añada los suyos. El
// pipeline respeta el logger que encuentra sin modificarlo: quien lo inyecta es
// dueno de sus atributos.
//
// Un ctx nil se sustituye por context.Background(): context.WithValue entra en
// panic con un padre nil, y un caso de uso no deberia poder tumbar el proceso
// por una llamada con un contexto mal construido. loggerDe ya tolera la
// ausencia de logger, asi que aqui solo hace falta que el contexto sea valido.
func ConLoggerEnContexto(ctx context.Context, logger *slog.Logger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	if logger == nil {
		return ctx
	}

	return context.WithValue(ctx, claveContextoLogger{}, logger)
}

// hayLoggerEnContexto indica si el contexto ya trae un logger.
//
// Lo necesita el pipeline para no duplicar el job_id: quien inyecta el logger
// desde fuera (la CLI) ya le pone sus atributos, y volver a anadirlo aqui
// produciria "job_id=job-123 job_id=job-123" en cada linea de log.
func hayLoggerEnContexto(ctx context.Context) bool {
	if ctx == nil {
		return false
	}

	logger, ok := ctx.Value(claveContextoLogger{}).(*slog.Logger)

	return ok && logger != nil
}

// loggerDe recupera el logger del contexto.
//
// Si no hay ninguno devuelve el logger por defecto, de modo que los casos de uso
// puedan loguear sin comprobar nada: la ausencia de logger nunca debe ser un
// error.
func loggerDe(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}

	logger, ok := ctx.Value(claveContextoLogger{}).(*slog.Logger)
	if !ok || logger == nil {
		return slog.Default()
	}

	return logger
}
