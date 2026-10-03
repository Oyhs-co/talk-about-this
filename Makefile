BINARY      := talkaboutthis
CMD_PKG     := ./cmd/talkaboutthis
BUILD_DIR   := bin
SCHEMA      := docs/specifications/backlog_schema.json
MAPPINGS    := configs/mappings.example.json
FIXTURES    := testdata
LDFLAGS     := -s -w

.DEFAULT_GOAL := help
.PHONY: help init build run test test-verbose cover lint fmt vet tidy clean check schema-check docker-build

help: ## Muestra esta ayuda
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

init: ## Descarga dependencias y prepara el directorio de salida
	@go mod download
	@go mod tidy
	@mkdir -p $(BUILD_DIR)

build: ## Compila el binario estatico en bin/
	@mkdir -p $(BUILD_DIR)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) $(CMD_PKG)
	@echo "OK -> $(BUILD_DIR)/$(BINARY)"

run: ## Ejecucion local en dry-run: make run FILE=ruta/transcripcion.md
	@test -n "$(FILE)" || (echo "Uso: make run FILE=ruta/transcripcion.md" && exit 1)
	go run $(CMD_PKG) ingest --file "$(FILE)" --dry-run --provider ollama

test: ## Ejecuta la suite de tests
	go test ./...

test-verbose: ## Tests con salida verbose
	go test -v ./...

cover: ## Tests con cobertura y perfil HTML en coverage.html
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "OK -> coverage.html"

fmt: ## Formatea el codigo
	go fmt ./...

vet: ## Analisis estatico basico
	go vet ./...

lint: ## Ejecuta golangci-lint si esta instalado; si no, cae a go vet
	@command -v golangci-lint >/dev/null 2>&1 \
		&& golangci-lint run ./... \
		|| { echo "golangci-lint no instalado; ejecutando 'go vet' en su lugar"; go vet ./...; }

tidy: ## Sincroniza go.mod y go.sum
	go mod tidy

schema-check: ## Valida que el JSON Schema de extraccion y los mapeos sean JSON validos
	@python scripts/validate_json.py $(SCHEMA) $(MAPPINGS)

check: fmt vet test ## Puerta de calidad: formato, vet y tests
	@echo "OK -> todas las verificaciones pasaron"

clean: ## Elimina artefactos de compilacion
	rm -rf $(BUILD_DIR) coverage.out coverage.html

docker-build: ## Compila imagen multi-stage para distribucion
	docker build -t $(BINARY):latest .