package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Tecla e: salva como CSV a tabela da tela (todas as linhas, na ordem e com a busca que estão na
// tela) ou, no painel, os candidatos e totais do lugar mostrado. O formato é o que o Excel em
// português e o Google Planilhas abrem direto: ";" entre as colunas, vírgula decimal e UTF-8 com
// BOM. Os números vão completos, sem as abreviações nem as opções de exibição da tela.

const pastaTabelas = "tabelas"

func (m *modelo) exportarCSV() tea.Cmd {
	nome, linhas := m.planilhaPainel()
	if m.aba != abaPainel {
		nome, linhas = m.planilhaTabela()
	}
	return func() tea.Msg {
		caminho := filepath.Join(pastaTabelas, nome+"-"+time.Now().Format("2006-01-02-15h04m05s")+".csv")
		if err := gravarCSV(caminho, linhas); err != nil {
			return msgSalvo{oque: "tabela", err: err}
		}
		return msgSalvo{oque: "tabela", caminho: caminho, detalhe: fmt.Sprintf("%d linhas", len(linhas)-1)}
	}
}

func gravarCSV(caminho string, linhas [][]string) error {
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		return err
	}
	arq, err := os.Create(caminho)
	if err != nil {
		return err
	}
	arq.WriteString("\xef\xbb\xbf") // BOM: sem ele, o Excel lê os acentos como Latin-1
	w := csv.NewWriter(arq)
	w.Comma, w.UseCRLF = ';', true
	w.WriteAll(linhas)
	if err := w.Error(); err != nil {
		arq.Close()
		return err
	}
	return arq.Close()
}

// planilhaTabela tem uma linha por linha da tabela da tela, com os dois turnos e a variação.
func (m *modelo) planilhaTabela() (string, [][]string) {
	cands, ok := m.candidatosSegundoTurno()
	if !ok {
		return m.nomeArquivo(), [][]string{{"Ainda não há dados dos candidatos do 2º turno"}}
	}
	nomes := [4]string{nomeProprio(primeiroNome(cands[0].Nome)), nomeProprio(primeiroNome(cands[1].Nome)), "brancos e nulos", "abstenção"}
	cab := []string{"Local", "Tipo", "UF", "Código do município no TSE"}
	for turno := 1; turno <= 2; turno++ {
		p := fmt.Sprintf("%dº turno: ", turno)
		cab = append(cab, p+"seções apuradas (%)", p+"eleitorado", p+"comparecimento", p+"abstenção",
			p+"abstenção (%)", p+"brancos", p+"nulos", p+"brancos e nulos (%)", p+"votos válidos",
			p+nomes[0]+" (votos)", p+nomes[0]+" (%)", p+nomes[1]+" (votos)", p+nomes[1]+" (%)", p+"dados de")
	}
	for _, n := range nomes {
		cab = append(cab, "variação "+n+" (pontos percentuais)")
	}

	linhas := [][]string{cab}
	for _, chave := range m.linhasOrdenadas() {
		linha := append([]string{}, m.descricaoLinha(chave)...)
		for turno := 1; turno <= 2; turno++ {
			r := m.dados[alvo{turno, chave}]
			if r == nil || r.SecoesTotalizadas == 0 {
				linha = append(linha, make([]string, 14)...)
				continue
			}
			c0, c1 := medidaDe(r, 0, cands), medidaDe(r, 1, cands)
			linha = append(linha, decimal2(r.PctSecoes), inteiro(r.Eleitorado), inteiro(r.Comparecimento),
				inteiro(r.Abstencao), decimal2(r.PctAbstencao), inteiro(r.Brancos), inteiro(r.Nulos),
				decimal2(medidaDe(r, 2, cands).pct), inteiro(r.Validos),
				inteiro(c0.qtd), decimal2(c0.pct), inteiro(c1.qtd), decimal2(c1.pct), r.Atualizacao)
		}
		r1, r2 := m.dados[alvo{1, chave}], m.dados[alvo{2, chave}]
		for i := range nomes {
			if r1 == nil || r2 == nil || r1.SecoesTotalizadas == 0 || r2.SecoesTotalizadas == 0 {
				linha = append(linha, "")
				continue
			}
			linha = append(linha, decimal2(medidaDe(r2, i, cands).pct-medidaDe(r1, i, cands).pct))
		}
		linhas = append(linhas, linha)
	}
	return m.nomeArquivo(), linhas
}

// descricaoLinha são as colunas que identificam a linha: nome completo, tipo, UF e código.
func (m *modelo) descricaoLinha(chave string) []string {
	uf := rotuloUF(chave[:2])
	switch {
	case chave == "br":
		return []string{"Brasil", "país", "", ""}
	case chave == "zz":
		return []string{"Exterior", "exterior", "", ""}
	case ehBairro(chave):
		return []string{m.nomeBairro(chave), "bairro (local de votação)", uf, chave[2:7]}
	case ehCidade(chave):
		return []string{m.nomeCidade(chave), "cidade", uf, chave[2:]}
	}
	return []string{nomesUF[chave], "estado", uf, ""}
}

// planilhaPainel tem os candidatos do lugar e do turno do painel e, abaixo, os totais.
func (m *modelo) planilhaPainel() (string, [][]string) {
	linhas := [][]string{{"Item", "Número", "Partido", "Quantidade", "%", "Local", "Turno", "Dados de"}}
	r := m.dados[m.alvoPainel()]
	if r == nil {
		return m.nomeArquivo(), linhas
	}
	local, turno := m.nomeAbrangencia(m.ufPainel), fmt.Sprintf("%dº", m.turnoPainel)
	for _, c := range r.Candidatos {
		linhas = append(linhas, []string{c.Nome, c.Numero, c.Partido, inteiro(c.Votos), decimal2(c.Pct), local, turno, r.Atualizacao})
	}
	for _, t := range []struct {
		nome string
		qtd  int64
		pct  float64
	}{
		{"Votos válidos", r.Validos, r.PctValidos}, {"Brancos", r.Brancos, r.PctBrancos}, {"Nulos", r.Nulos, r.PctNulos},
		{"Comparecimento", r.Comparecimento, r.PctComparecimento}, {"Abstenção", r.Abstencao, r.PctAbstencao},
		{"Seções totalizadas", r.SecoesTotalizadas, r.PctSecoes},
	} {
		linhas = append(linhas, []string{t.nome, "", "", inteiro(t.qtd), decimal2(t.pct), local, turno, r.Atualizacao})
	}
	linhas = append(linhas, []string{"Eleitorado", "", "", inteiro(r.Eleitorado), "", local, turno, r.Atualizacao})
	return m.nomeArquivo(), linhas
}

// nomeArquivo descreve o que foi exportado: "por-estado", "cidades-sp", "bairros-sp-campinas",
// "painel-2turno-brasil".
func (m *modelo) nomeArquivo() string {
	var partes []string
	switch {
	case m.aba == abaPainel:
		partes = []string{"painel", fmt.Sprintf("%dturno", m.turnoPainel), m.nomeAbrangencia(m.ufPainel)}
	case m.modoBairros():
		partes = []string{"bairros", m.ufCidades, m.nomeCidade(m.cidadeBairros)}
	case m.aba == abaCidades:
		partes = []string{"cidades", m.ufCidades}
	default:
		partes = []string{"por-estado"}
	}
	if m.busca.Value() != "" && m.aba == abaCidades {
		partes = append(partes, "busca", m.busca.Value())
	}
	// Só letras sem acento e números, separados por hífen.
	nome := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return ' '
	}, strings.ToLower(paraBusca(strings.Join(partes, " "))))
	return strings.Join(strings.Fields(nome), "-")
}

func inteiro(n int64) string { return strconv.FormatInt(n, 10) }

// decimal2 escreve com duas casas e vírgula, sem separador de milhar, para a planilha ler como número.
func decimal2(f float64) string { return fmtCasas(f, 2) }
