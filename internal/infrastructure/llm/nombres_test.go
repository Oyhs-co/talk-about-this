package llm_test

import (
	"strings"
	"testing"

	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/llm"
)

// Estos tests cubren Name() en los cuatro proveedores.
//
// Name() no es decorativo: su valor aparece en el log de cada intento de
// extraccion y en el mensaje de error cuando el proveedor falla. Si devuelve
// cadena vacia, el diagnostico dice "el proveedor fallo" sin decir cual, que es
// el peor caso posible cuando hay tres modelos locales cargados.

// TestTodosLosProveedoresImplementanElPuerto es una comprobacion de contrato:
// los cuatro adaptadores deben satisfacer domain.LLMProvider.
//
// La asignacion a un mapa de tipo interfaz es lo que la hace REAL: si un
// proveedor nuevo quedara a medio implementar, el test no compila.
func TestTodosLosProveedoresImplementanElPuerto(t *testing.T) {
	proveedores := map[string]domain.LLMProvider{
		"ollama":    llm.NuevaOllama(llm.OllamaOpciones{}),
		"openai":    llm.NuevaOpenAI(llm.OpenAIOpciones{}),
		"anthropic": llm.NuevaAnthropic(llm.AnthropicOpciones{}),
		"gemini":    llm.NuevaGemini(llm.GeminiOpciones{}),
	}

	if len(proveedores) != 4 {
		t.Fatalf("se esperaban 4 proveedores, se registraron %d", len(proveedores))
	}

	for nombre, proveedor := range proveedores {
		t.Run(nombre, func(t *testing.T) {
			identidad := proveedor.Name()

			if identidad == "" {
				t.Fatal("Name() devolvio cadena vacia")
			}

			if !strings.HasPrefix(identidad, nombre+"/") {
				t.Errorf("Name() = %q, se esperaba el prefijo %q", identidad, nombre+"/")
			}

			// El modelo concreto debe aparecer: es lo que permite distinguir
			// "respondio qwen2.5" de "respondio llama3".
			modelo := strings.TrimPrefix(identidad, nombre+"/")
			if modelo == "" {
				t.Errorf("Name() = %q no incluye el nombre del modelo", identidad)
			}
		})
	}
}

// TestProveedoresUsanElModeloIndicado comprueba que el modelo configurado llega
// al nombre, y que un modelo vacio cae en el valor por defecto.
func TestProveedoresUsanElModeloIndicado(t *testing.T) {
	casos := []struct {
		nombre    string
		proveedor domain.LLMProvider
		esperado  string
	}{
		{"ollama", llm.NuevaOllama(llm.OllamaOpciones{Modelo: "qwen2.5"}), "ollama/qwen2.5"},
		{"openai", llm.NuevaOpenAI(llm.OpenAIOpciones{Modelo: "gpt-4o"}), "openai/gpt-4o"},
		{"anthropic", llm.NuevaAnthropic(llm.AnthropicOpciones{Modelo: "claude-sonnet-4"}), "anthropic/claude-sonnet-4"},
		{"gemini", llm.NuevaGemini(llm.GeminiOpciones{Modelo: "gemini-1.5-pro"}), "gemini/gemini-1.5-pro"},
	}

	for _, tt := range casos {
		t.Run(tt.nombre, func(t *testing.T) {
			if obtenido := tt.proveedor.Name(); obtenido != tt.esperado {
				t.Errorf("Name() = %q, se esperaba %q", obtenido, tt.esperado)
			}
		})
	}
}

// TestProveedoresConModeloVacioUsanElValorPorDefecto comprueba que no queda un
// nombre con la barra colgando, tipo "ollama/".
func TestProveedoresConModeloVacioUsanElValorPorDefecto(t *testing.T) {
	proveedores := map[string]domain.LLMProvider{
		"ollama":    llm.NuevaOllama(llm.OllamaOpciones{}),
		"openai":    llm.NuevaOpenAI(llm.OpenAIOpciones{}),
		"anthropic": llm.NuevaAnthropic(llm.AnthropicOpciones{}),
		"gemini":    llm.NuevaGemini(llm.GeminiOpciones{}),
	}

	for nombre, proveedor := range proveedores {
		t.Run(nombre, func(t *testing.T) {
			identidad := proveedor.Name()

			if strings.HasSuffix(identidad, "/") {
				t.Errorf("Name() = %q: el modelo por defecto no se resolvio", identidad)
			}
		})
	}
}
