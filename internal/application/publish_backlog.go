package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"talkaboutthis/internal/domain"
)

// Modo de salida de la etapa de despacho.
type ModoSalida string

const (
	// ModoDryRun imprime el backlog y no publica nada (RF-06).
	//
	// La garantia de este modo es ABSOLUTA: cero llamadas de red hacia la
	// plataforma destino. TC-05 lo verifica, y por eso el caso de uso
	// comprueba el modo ANTES de resolver identidades, que es la primera
	// operacion capable de tocar una API externa.
	ModoDryRun ModoSalida = "dry-run"

	// ModoPublish crea las tarjetas en el tablero destino (RF-05).
	ModoPublish ModoSalida = "publish"
)

// PoliticaDesconocido indica que hacer con un item cuyo responsable no se pudo
// resolver.
//
// Se declara aqui, en la capa de aplicacion, y no en el dominio: es una
// decision de orquestacion. El adaptador de identidad solo sabe si resuelve o
// no.
type PoliticaDesconocido string

const (
	// AnteDesconocidoFallar detiene la publicacion.
	//
	// Es el valor por defecto y el recomendado. Publicar una tarea bajo la
	// persona equivocada, o sin asignar, es un fallo silencioso que alguien
	// descubre semanas despues.
	AnteDesconocidoFallar PoliticaDesconocido = "fail"

	// AnteDesconocidoSinAsignado publica el item sin responsable.
	AnteDesconocidoSinAsignado PoliticaDesconocido = "assign_unassigned"

	// AnteDesconocidoOmitir descarta el item.
	AnteDesconocidoOmitir PoliticaDesconocido = "skip"
)

// ResolvedorDeIdentidades resuelve nombres a handles.
//
// Es el puerto domain.IdentityMapper. Se reexporta un alias para que el nombre
// que aparece en los tests de esta capa sea explicito.
type ResolvedorDeIdentidades = domain.IdentityMapper

// ResumenDespacho es el resultado de la etapa de despacho.
type ResumenDespacho struct {
	// Items son los items que se consideran publicar, ya con handle resuelto.
	Items []domain.ActionItem
	// Omitidos son los items descartados por la politica "skip".
	Omitidos []domain.ActionItem
	// Publicados es el resultado de la plataforma destino. Vacio en dry-run.
	Publicados []domain.PublishResult
	// Modo indica si hubo publicacion real.
	Modo ModoSalida
}

// ResumenDePublicacion cuenta los exitos y fallos, para el log final.
func (r ResumenDespacho) ResumenDePublicacion() (exitos, fallos int) {
	for _, resultado := range r.Publicados {
		if resultado.Success {
			exitos++
		} else {
			fallos++
		}
	}
	return
}

// PublicarBacklog resuelve identidades y despacha el backlog (RF-04, RF-05, RF-06).
//
// ORDEN DE LAS OPERACIONES, Y POR QUE
//
//  1. Si el modo es dry-run, se devuelve de inmediato. Es la garantia de TC-05:
//     en dry-run no se llama ni al mapper (que podria consultar una API) ni al
//     adaptador. Verificar el modo primero, y no "no publicar al final", es lo
//     que hace la garantia estructural en lugar de incidental.
//
//  2. Se resuelve cada responsable. Se acumulan los errores en lugar de fallar
//     en el primero, para poder informar de todos los nombres no reconocidos de
//     una sola vez.
//
//  3. Se aplica la politica de nombres desconocidos.
//
//  4. Se invoca el adaptador, que ya no tiene que decidir nada.
type PublicarBacklog struct {
	identidades domain.IdentityMapper
	tablero     domain.ProjectBoardAdapter
	politica    PoliticaDesconocido
	// handlePorDefecto es el responsable asignado con la politica
	// assign_unassigned.
	handlePorDefecto string
}

// PublicarOpciones configura el caso de uso.
type PublicarOpciones struct {
	// Identidades resuelve nombres. Obligatorio.
	Identidades domain.IdentityMapper
	// Tablero publica. Es OBLIGATORIO incluso en dry-run para que la interfaz
	// no cambie entre modos, pero no se invoca nunca en dry-run.
	Tablero domain.ProjectBoardAdapter
	// Politica ante nombres no resueltos. Por defecto, fallar.
	Politica PoliticaDesconocido
	// HandlePorDefecto para la politica assign_unassigned.
	HandlePorDefecto string
}

// NuevoPublicarBacklog construye el caso de uso.
func NuevoPublicarBacklog(opciones PublicarOpciones) (*PublicarBacklog, error) {
	if opciones.Identidades == nil {
		return nil, fmt.Errorf("%w: falta el resolutor de identidades", domain.ErrConfigInvalida)
	}

	if opciones.Tablero == nil {
		return nil, fmt.Errorf("%w: falta el adaptador de tablero", domain.ErrConfigInvalida)
	}

	politica := opciones.Politica
	if politica == "" {
		politica = AnteDesconocidoFallar
	}

	switch politica {
	case AnteDesconocidoFallar, AnteDesconocidoSinAsignado, AnteDesconocidoOmitir:
	default:
		return nil, fmt.Errorf("%w: politica de assignee desconocido invalida: %q",
			domain.ErrConfigInvalida, politica)
	}

	return &PublicarBacklog{
		identidades:      opciones.Identidades,
		tablero:          opciones.Tablero,
		politica:         politica,
		handlePorDefecto: opciones.HandlePorDefecto,
	}, nil
}

// Ejecutar resuelve identidades y despacha segun el modo.
func (p *PublicarBacklog) Ejecutar(ctx context.Context, projectRef string, items []domain.ActionItem, modo ModoSalida) (*ResumenDespacho, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// GARANTIA DE TC-05: en dry-run no se ejecuta ninguna operacion capaz de
	// hacer I/O. Se comprueba el modo en la PRIMERA instruccion util, antes de
	// tocar identidades o tablero.
	//
	// Un test verifica que un adaptador que entre en panic si se invoca no llega
	// a hacerlo en este camino.
	if modo == ModoDryRun {
		// Se devuelven los items tal cual, sin resolver identidades: el dry-run
		// muestra lo que el LLM extrajo, que es exactamente lo que el usuario
		// quiere revisar antes de publicar nada.
		loggerDe(ctx).Info("modo dry-run: no se publica nada",
			slog.Int("items", len(items)),
		)

		return &ResumenDespacho{Items: items, Modo: ModoDryRun}, nil
	}

	if modo != ModoPublish {
		return nil, fmt.Errorf("%w: modo de salida desconocido: %q", domain.ErrConfigInvalida, modo)
	}

	preparados, omitidos, err := p.resolverIdentidades(ctx, items)
	if err != nil {
		return nil, err
	}

	if len(preparados) == 0 {
		return &ResumenDespacho{
			Items:    nil,
			Omitidos: omitidos,
			Modo:     modo,
		}, nil
	}

	// Se invoca el adaptador. Este es el UNICO punto del flujo donde se hace una
	// mutacion remota, y solo se alcanza si el modo es publish.
	publicados, err := p.tablero.PublishBacklog(ctx, projectRef, preparados)
	if err != nil {
		return nil, fmt.Errorf("no se pudo publicar el backlog: %w", err)
	}

	exitos, fallos := 0, 0
	for _, resultado := range publicados {
		if resultado.Success {
			exitos++
		} else {
			fallos++
		}
	}

	loggerDe(ctx).Info("publicacion terminada",
		slog.String("plataforma", p.tablero.PlatformName()),
		slog.Int("publicados", exitos),
		slog.Int("fallidos", fallos),
		slog.Int("omitidos", len(omitidos)),
	)

	// Un fallo parcial NO es un error de la operacion: el resumen lo refleja.
	// Devolver error aqui haria que el usuario creyera que no se publico nada,
	// cuando en realidad cinco de seis tarjetas existen.
	if fallos > 0 {
		loggerDe(ctx).Warn("algunos items no se publicaron",
			slog.Int("fallidos", fallos),
		)
	}

	return &ResumenDespacho{
		Items:      preparados,
		Omitidos:   omitidos,
		Publicados: publicados,
		Modo:       modo,
	}, nil
}

// resolverIdentidades resuelve los responsables y aplica la politica.
//
// Acumula los nombres no reconocidos en lugar de fallar en el primero: el
// usuario puede corregir toda la tabla de una vez en lugar de arreglar un
// nombre, reejecutar y descubrir el siguiente.
func (p *PublicarBacklog) resolverIdentidades(ctx context.Context, items []domain.ActionItem) (preparados, omitidos []domain.ActionItem, err error) {
	preparados = make([]domain.ActionItem, 0, len(items))
	omitidos = make([]domain.ActionItem, 0)

	desconocidos := make([]string, 0)

	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}

		handle, errRes := p.identidades.ResolveHandle(ctx, item.RawAssignee)
		if errRes != nil {
			if !errors.Is(errRes, domain.ErrIdentidadNoResuelta) {
				// Un fallo que no es "no resoluble" (contexto cancelado, error de
				// red del mapper) se propaga: no se puede aplicar una politica
				// sobre un error operativo.
				return nil, nil, fmt.Errorf("no se pudo resolver la identidad de %q: %w",
					item.RawAssignee, errRes)
			}

			desconocidos = append(desconocidos, item.RawAssignee)

			switch p.politica {
			case AnteDesconocidoOmitir:
				omitidos = append(omitidos, item)
				continue

			case AnteDesconocidoSinAsignado:
				item.MappedHandle = p.handlePorDefecto

			case AnteDesconocidoFallar:
				// Se acumulan; el error se emite al final del recorrido.
				continue
			}
		} else {
			item.MappedHandle = handle
		}

		preparados = append(preparados, item)
	}

	if len(desconocidos) > 0 && p.politica == AnteDesconocidoFallar {
		return nil, nil, fmt.Errorf(
			"%w: %d responsable(s) sin handle en la tabla de aliases: %s",
			domain.ErrIdentidadNoResuelta, len(desconocidos), unir(desconocidos))
	}

	return preparados, omitidos, nil
}

// unir concatena nombres para un mensaje de error legible.
func unir(valores []string) string {
	const max = 10

	if len(valores) > max {
		return fmt.Sprintf("%v (y %d mas)", valores[:max], len(valores)-max)
	}

	salida := ""
	for i, v := range valores {
		if i > 0 {
			salida += ", "
		}
		salida += v
	}

	return salida
}
