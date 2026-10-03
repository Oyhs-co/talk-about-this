package domain

import (
	"context"
	"io"
)

// Este archivo define los PUERTOS del dominio: las interfaces que el nucleo
// necesita del exterior. Son deliberadamente pequenas y orientadas a un solo
// proposito.
//
// Que se pueda anadir una plataforma de project management, un proveedor de LLM
// o un formato de documento sin modificar este archivo es el objetivo del
// Principio Open-Closed. Para soportar otra plataforma, el procedimiento es:
//
//  1. Escribir el adaptador en internal/infrastructure/adapters/.
//  2. Implementar la interfaz correspondiente.
//  3. Declarar la asercion de compilacion var _ domain.ProjectBoardAdapter = (*MiAdaptador)(nil).
//  4. Registrarlo en el composition root (cmd/talkaboutthis).
//
// NO se modifica internal/domain. El caso TC-07 verifica precisamente esto.

// DocumentParser define el contrato para extraer texto plano de cualquier
// formato de entrada.
//
// CanParse permite registrar varios parsers y que la aplicacion elija el
// adecuado por extension; debe ser case-insensitive. Parse recibe un
// io.Reader, no una ruta, para que el dominio no dependa del sistema de
// archivos y los tests puedan inyectar un reader en memoria.
//
// Quien llame a Parse es dueno del reader y debe cerrarlo. Parse no debe
// cerrarlo, para permitir las implementaciones que envuelven el reader.
type DocumentParser interface {
	CanParse(extension string) bool
	Parse(ctx context.Context, reader io.Reader) (string, error)
}

// LLMProvider define el contrato para comunicarse con cualquier motor de IA,
// local o remoto.
//
// Name identifica al proveedor en los logs estructurados (RNF-06).
// GenerateStructuredOutput debe devolver el payload ya decodificado en JSON
// bruto. No le corresponde validar el schema: la validacion y el Retry Loop con
// autocorreccion (RF-07) son responsabilidad de la capa de aplicacion, que
// necesita inspeccionar el error concreto para reintentarlo.
//
// Implementaciones: internal/infrastructure/llm/{ollama,openai,anthropic,gemini}.go
type LLMProvider interface {
	Name() string
	GenerateStructuredOutput(ctx context.Context, prompt string, schemaJSON string) ([]byte, error)
}

// IdentityMapper mapea los nombres hallados en la reunion con handles de la
// plataforma destino.
//
// rawName es el nombre tal como aparece en la minuta ("Omar Hernández") y el
// resultado es el handle ("omarhernan"). La implementacion de referencia es
// case-insensitive.
//
// Debe respetar ctx en toda resolucion que requiera red y respetar el contexto
// de error: si no puede resolver, debe envolver ErrIdentidadNoResuelta para que
// la politica de unknowns configurada pueda aplicarse.
type IdentityMapper interface {
	ResolveHandle(ctx context.Context, rawName string) (string, error)
}

// ProjectBoardAdapter define el contrato para publicar items de backlog en
// cualquier gestor de proyectos.
//
// PlatformName identifica la plataforma en logs y mensajes de error.
// PublishBacklog debe ser tolerante a fallos parciales: devolver un
// []PublishResult donde cada elemento refleje el exito o el fallo de cada item,
// en lugar de abortar en el primer error. El llamador necesita saber cuantas
// tarjetas se crearon.
//
// La implementacion NO debe dry-runear por su cuenta: esa decision ya fue
// tomada antes de invocar el puerto (RF-06).
//
// Implementaciones: internal/infrastructure/adapters/{github_graphql,github_cli,jira,linear}.go
type ProjectBoardAdapter interface {
	PlatformName() string
	PublishBacklog(ctx context.Context, projectRef string, items []ActionItem) ([]PublishResult, error)
}
