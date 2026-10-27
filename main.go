// Comando eleicoes acompanha em tempo real a apuração para Presidente com os dados
// públicos do TSE: um painel ao vivo e uma tabela por estado comparando os dois turnos.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const intervaloMinimoTSE = 15

// versao vem da compilação, com -ldflags "-X main.versao=v1.0.0" (make build e GoReleaser).
var versao = "dev"

// versaoPrograma é a versão da compilação ou, num "go install", a do módulo.
func versaoPrograma() string {
	if info, ok := debug.ReadBuildInfo(); ok && versao == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return versao
}

const textoAjuda = `Uso: eleicoes [opções]

Acompanha em tempo real a apuração para Presidente (dados públicos do TSE).

Opções:
  -t, -turno N          turno exibido no painel: 1 ou 2 (padrão: 2)
  -u, -uf UF            abrangência do painel: br (padrão), sigla do estado ou zz (exterior)
  -i, -intervalo S      segundos entre consultas (padrão: 60; mínimo de 15 no TSE)
  -1, -uma-vez          consulta uma vez, imprime o painel e a tabela e sai
      -apresentacao S   liga a apresentação após S segundos sem tecla (padrão: 0, desligado)
      -troca S          segundos de cada tela no modo apresentação (padrão: 30)
      -ciclo C          ciclo eleitoral (padrão: ele2026)
      -base URL         URL base dos dados (padrão: TSE); para testar, a do simulacao/servidor.py
      -bairros DIR      pasta dos resultados por bairro gerados por "make bairros" (padrão: os que
                        vêm no programa)
      -versao           mostra a versão e sai

Telas: 1 Painel · 2 Por estado · 3 Cidades (as cidades de um estado, com busca pelo nome).

Modo apresentação (desligado por padrão): com a tecla p, ou sozinho após -apresentacao S
segundos sem tecla, o programa alterna entre o painel e a tabela por estado a cada 30 s, com a
tela limpa para transmissão. Qualquer tecla volta ao modo interativo.

Teclas: tab, 1, 2 e 3 trocam de tela · ↑↓ rolam · ←→ mudam as colunas da tabela
        enter abre o estado ou a cidade no painel · t alterna o turno do painel
        c abre as cidades do estado selecionado na tabela por estado
        v alterna a variação (pontos ou %) · o mostra/oculta os votos do 1º turno
        m alterna os votos entre a quantidade inteira e milhares
        s muda a ordem: UF ou nome, % apurado, % de cada candidato, votos a vir (só estados)
        f tira uma foto da tela (PNG em fotos/, copiado para colar com ctrl+v)
        e salva a tabela da tela inteira (ou os candidatos do painel) como CSV em tabelas/
        p entra no modo apresentação na hora · r atualiza agora · q sai
Cidades: ←→ trocam o estado · shift+←→ mudam as colunas · / busca pelo nome (sem acentos)
        enter ou esc fecham a busca; esc apaga o texto buscado
        b abre os bairros da cidade selecionada; esc ou b voltam para as cidades
Bairros: resultado por bairro do local de votação, montado com os dados abertos do TSE pelo
        "make bairros" (baixa ~500 MB) e embutido no programa; com -bairros, r relê os
        arquivos da pasta depois de gerá-los de novo
`

type config struct {
	turno        int
	uf           string
	intervalo    int
	umaVez       bool
	apresentacao int // segundos sem tecla até o modo apresentação; 0 desliga
	troca        int // segundos de cada aba no modo apresentação
	ciclo        string
	base         string
	pastaBairros string // vazia: os dados embutidos
	versao       bool
}

func lerArgumentos(args []string) (config, error) {
	var cfg config
	fs := flag.NewFlagSet("eleicoes", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // as mensagens do pacote flag vêm em inglês; ver traduzirErro
	for _, nome := range []string{"turno", "t"} {
		fs.IntVar(&cfg.turno, nome, 2, "")
	}
	for _, nome := range []string{"uf", "u"} {
		fs.StringVar(&cfg.uf, nome, "br", "")
	}
	for _, nome := range []string{"intervalo", "i"} {
		fs.IntVar(&cfg.intervalo, nome, 60, "")
	}
	for _, nome := range []string{"uma-vez", "1"} {
		fs.BoolVar(&cfg.umaVez, nome, false, "")
	}
	fs.IntVar(&cfg.apresentacao, "apresentacao", 0, "")
	fs.IntVar(&cfg.troca, "troca", 30, "")
	fs.StringVar(&cfg.ciclo, "ciclo", "ele2026", "")
	fs.StringVar(&cfg.base, "base", baseTSE, "")
	fs.StringVar(&cfg.pastaBairros, "bairros", "", "")
	fs.BoolVar(&cfg.versao, "versao", false, "")
	if err := fs.Parse(args); err != nil {
		return cfg, traduzirErro(err)
	}

	cfg.uf = strings.ToLower(cfg.uf)
	cfg.base = strings.TrimRight(cfg.base, "/")
	minimo := 1
	if cfg.base == baseTSE {
		minimo = intervaloMinimoTSE
	}
	switch {
	case fs.NArg() > 0:
		return cfg, fmt.Errorf("argumento inesperado: %s", fs.Arg(0))
	case cfg.turno != 1 && cfg.turno != 2:
		return cfg, errors.New("-turno deve ser 1 ou 2")
	case nomesUF[cfg.uf] == "":
		return cfg, fmt.Errorf("-uf desconhecida: %s (use br, a sigla de um estado ou zz)", cfg.uf)
	case cfg.intervalo < minimo:
		return cfg, fmt.Errorf("-intervalo mínimo é %d segundo(s)", minimo)
	case cfg.apresentacao < 0:
		return cfg, errors.New("-apresentacao não pode ser negativo (use 0 para desligar)")
	case cfg.troca < 1:
		return cfg, errors.New("-troca deve ser de pelo menos 1 segundo")
	}
	return cfg, nil
}

func traduzirErro(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return err
	}
	msg := err.Error()
	for ingles, portugues := range map[string]string{
		"flag provided but not defined: ": "opção desconhecida: ",
		"flag needs an argument: ":        "falta o valor da opção ",
		"invalid value ":                  "valor inválido ",
		" for flag ":                      " para a opção ",
		": parse error":                   "",
		"invalid boolean value ":          "valor inválido ",
	} {
		msg = strings.ReplaceAll(msg, ingles, portugues)
	}
	return errors.New(msg + " (use -h para ver as opções)")
}

func terminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// executarUmaVez consulta tudo uma vez e imprime o painel e a tabela completos, sem TUI.
func executarUmaVez(cfg config, cli *Cliente) int {
	m := novoModelo(cfg, cli)
	if terminal(os.Stdin) && terminal(os.Stdout) {
		m.est = novosEstilos(lipgloss.HasDarkBackground(os.Stdin, os.Stdout))
	}
	ctx, cancelar := context.WithTimeout(context.Background(), time.Minute)
	defer cancelar()
	m.aplicar(cli.buscarTudo(ctx, cfg.ciclo, nil, nil, false))

	var partes []string
	if m.simulacao() {
		partes = append(partes, m.est.simulacao.Render(m.faixaSimulacao()))
	}
	partes = append(partes, m.renderPainel(100))
	if t, ok := m.montarTabela(); ok {
		const semLimite = 1 << 20
		partes = append(partes, m.resumoApuracao()+"\n"+t.render(m.est, semLimite, semLimite, 0, 0, -1),
			m.est.fraco.Render(strings.Join(m.legendaTabela(false), "\n")))
	}
	if m.aviso != "" {
		partes = append(partes, m.est.aviso.Render("Aviso: "+m.aviso))
	}
	lipgloss.Println(strings.Join(partes, "\n\n"))
	if m.dados[m.alvoPainel()] == nil {
		return 1
	}
	return 0
}

func main() {
	cfg, err := lerArgumentos(os.Args[1:])
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Print(textoAjuda)
		return
	case err != nil:
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(2)
	case cfg.versao:
		fmt.Println("eleicoes", versaoPrograma())
		return
	}

	cli := novoCliente(cfg.base)
	if cfg.umaVez {
		os.Exit(executarUmaVez(cfg, cli))
	}
	if _, err := tea.NewProgram(novoModelo(cfg, cli)).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}
