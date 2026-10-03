// Package domain contiene el nucleo del negocio de TalkAboutThis: entidades,
// value objects, errores y los puertos (interfaces) que definen los contratos
// de entrada y salida.
//
// REGLA ARQUITECTONICA INNEGOCIABLE (AGENTS.md seccion 1):
// Este paquete solo puede importar la biblioteca estandar de Go. Jamas debe
// importar internal/infrastructure ni internal/application, ni ninguna
// libreria de terceros. El test TestDomainNoImportaInfrastructure lo verifica
// de forma automatica: cualquier violacion rompe la compilacion del test, no
// una revision manual.
package domain

import "errors"

// Errores sentinela del dominio. Se declaran con errors.New (no con fmt) porque
// son valores constantes que se comparan con errors.Is. El contexto variable se
// agrega en el punto de uso con fmt.Errorf("...: %w", err).
var (
	// ErrPriorityInvalida indica que un valor de Priority esta fuera del enum
	// HIGH | MEDIUM | LOW. Nunca se sustituye por un valor por defecto en
	// silencio: un default ocultaria un error del LLM o de mapeo.
	ErrPriorityInvalida = errors.New("prioridad invalida: se esperaba HIGH, MEDIUM o LOW")

	// ErrTituloInvalido indica que el titulo de un ActionItem esta vacio o
	// excede MaxTituloLen caracteres.
	ErrTituloInvalido = errors.New("titulo de accion invalido")

	// ErrDescripcionInvalida indica que la descripcion de un ActionItem esta
	// vacia. Un item sin descripcion no es accionable.
	ErrDescripcionInvalida = errors.New("descripcion de accion invalida")

	// ErrAsignadoInvalido indica que assignee_name esta vacio en el origen. El
	// campo debe contener el nombre real o la mencion tal como aparece en la
	// minuta; se resuelve a un handle despues, en la etapa de identidad.
	ErrAsignadoInvalido = errors.New("asignado invalido: assignee_name vacio")

	// ErrSchemaViolation indica que la respuesta del LLM no valida contra
	// docs/specifications/backlog_schema.json. La cadena envuelta debe contener
	// el error concreto de validacion para alimentar el prompt de autocorreccion
	// del Retry Loop (RF-07).
	ErrSchemaViolation = errors.New("la respuesta no cumple el JSON Schema de extraccion")

	// ErrIdentidadNoResuelta indica que el IdentityMapper no encontro un handle
	// para un nombre presente en la minuta.
	ErrIdentidadNoResuelta = errors.New("no se pudo resolver un handle para el nombre indicado")

	// ErrFormatoNoSoportado indica que ningun DocumentParser registrado puede
	// procesar la extension o la cabecera del archivo de entrada.
	ErrFormatoNoSoportado = errors.New("formato de documento no soportado")

	// ErrMapeoDeIdentidadInvalido indica que la tabla de aliases (mappings.json)
	// esta mal formada o no se pudo cargar.
	ErrMapeoDeIdentidadInvalido = errors.New("la tabla de mapeo de identidades es invalida")

	// ErrConfigInvalida indica que falta una variable de entorno o flag
	// obligatorio para operar con el proveedor o adaptador seleccionado.
	ErrConfigInvalida = errors.New("configuracion invalida o incompleta")

	// ErrPublicacionFallida indica que un adaptador no pudo publicar uno o varios
	// items en la plataforma destino.
	ErrPublicacionFallida = errors.New("fallo al publicar el backlog en la plataforma destino")

	// ErrReintentosAgotados indica que el Retry Loop supero el maximo de
	// intentos permitido sin obtener una respuesta valida del LLM.
	ErrReintentosAgotados = errors.New("se agotaron los reintentos sin obtener una respuesta valida")
)
