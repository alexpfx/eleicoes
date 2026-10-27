# Acompanhamento da apuração para Presidente (Go) e a simulação para testes (Python).
# Rode "make help" para ver os alvos.

BINARIO   := eleicoes
VERSAO    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PYTHON    ?= $(if $(wildcard venv/bin/python),venv/bin/python,python3)
SIMULACAO := http://127.0.0.1:8000/oficial
PASSO     ?= 10
INTERVALO ?= 5

.DEFAULT_GOAL := build
.PHONY: build run uma-vez simular simulado gerar-simulacao bairros apagar-cache fmt check dist clean help

# Sempre chama o go build: o cache do Go deixa a recompilação quase instantânea quando nada mudou.
build: ## compila o programa (alvo padrão), com os dados de dados/bairros embutidos
	go build -ldflags "-X main.versao=$(VERSAO)" -o $(BINARIO) .
	@echo "Compilado: ./$(BINARIO)"

run: build ## compila e abre o programa com os dados do TSE
	./$(BINARIO)

uma-vez: build ## consulta o TSE uma vez e imprime o painel e a tabela
	./$(BINARIO) -1

simular: ## sobe o servidor de simulação (PASSO=segundos por etapa, padrão 10)
	$(PYTHON) simulacao/servidor.py --passo $(PASSO)

simulado: build ## abre o programa contra o servidor de simulação (INTERVALO=segundos, padrão 5)
	./$(BINARIO) -base $(SIMULACAO) -i $(INTERVALO)

gerar-simulacao: ## gera de novo os dados fictícios em simulacao/dados
	$(PYTHON) simulacao/gerar.py

bairros: ## baixa os dados abertos do TSE (~500 MB, só o que mudou) e gera dados/bairros (entra no próximo build)
	go run ./cmd/bairros

apagar-cache: ## apaga os arquivos do TSE baixados por "make bairros" (dados/cache)
	rm -rf dados/cache

fmt: ## formata o código Go
	gofmt -w .

check: ## confere a formatação e roda o go vet
	@test -z "$$(gofmt -l .)" || { echo "Arquivos sem gofmt:"; gofmt -l .; exit 1; }
	go vet ./...

# O Release de verdade sai pelo GitHub Actions a cada tag v* (.github/workflows/release.yml).
dist: ## gera em dist/ os pacotes de todos os sistemas, sem publicar (precisa do goreleaser)
	goreleaser release --snapshot --clean

clean: ## apaga o binário e a pasta dist/
	rm -rf $(BINARIO) dist

help: ## lista os alvos
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{ printf "  %-16s %s\n", $$1, $$2 }'
