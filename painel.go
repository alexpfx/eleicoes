package main

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// renderPainel é a versão do painel do antigo main.py: andamento, candidatos e totais.
func (m *modelo) renderPainel(largura int) string {
	e := m.est
	linhas := []string{e.titulo.Render(fmt.Sprintf("Presidente · %dº turno · %s", m.turnoPainel, m.nomeAbrangencia(m.ufPainel)))}
	if m.eleicoes != nil && m.eleicoes.Nome[m.turnoPainel] != "" {
		linhas = append(linhas, e.fraco.Render(m.eleicoes.Nome[m.turnoPainel]))
	}

	r := m.dados[m.alvoPainel()]
	if r == nil {
		msg := "Ainda não há dados disponíveis."
		if m.eleicoes != nil && m.eleicoes.Codigo[m.turnoPainel] == 0 {
			msg = fmt.Sprintf("O %dº turno não está na configuração da fonte de dados.", m.turnoPainel)
		}
		return strings.Join(append(linhas, "", e.fraco.Render(msg)), "\n")
	}

	origem := "pelo TSE"
	if m.simulacao() {
		origem = "pela simulação"
	}
	linhas = append(linhas,
		e.fraco.Render(fmt.Sprintf("Atualizado %s em %s", origem, r.Atualizacao)),
		"",
		fmt.Sprintf("%s  %s de %s (%s)", e.negrito.Render("Seções totalizadas"),
			fmtInt(r.SecoesTotalizadas), fmtInt(r.Secoes), fmtPct2(r.PctSecoes)),
		barraSecoes(r.PctSecoes, min(largura-1, 72)),
		"",
	)
	linhas = append(linhas, m.linhasCandidatos(r, largura)...)

	if len(r.Candidatos) >= 2 && r.Candidatos[0].Votos > 0 {
		a, b := r.Candidatos[0], r.Candidatos[1]
		linhas = append(linhas, "", fmt.Sprintf("Vantagem de %s: %s votos (%s pontos percentuais)",
			lipgloss.NewStyle().Bold(true).Foreground(m.corCandidato(a.Numero)).Render(a.Nome),
			e.negrito.Render(fmtInt(a.Votos-b.Votos)), fmtCasas(a.Pct-b.Pct, 2)))
	}

	itens := [6][3]string{
		{"Votos válidos", fmtInt(r.Validos), fmtPct2(r.PctValidos)},
		{"Brancos", fmtInt(r.Brancos), fmtPct2(r.PctBrancos)},
		{"Nulos", fmtInt(r.Nulos), fmtPct2(r.PctNulos)},
		{"Comparecimento", fmtInt(r.Comparecimento), fmtPct2(r.PctComparecimento)},
		{"Abstenção", fmtInt(r.Abstencao), fmtPct2(r.PctAbstencao)},
		{"Eleitorado", fmtInt(r.Eleitorado), ""},
	}
	item := func(it [3]string) string {
		return e.fraco.Render(alinharEsquerda(it[0], 15)) + e.negrito.Render(alinharDireita(it[1], 12)) +
			"  " + alinharDireita(it[2], 7)
	}
	linhas = append(linhas, "")
	if largura >= 2*36+4 {
		for i := range 3 {
			linhas = append(linhas, item(itens[i])+"    "+item(itens[i+3]))
		}
	} else {
		for _, it := range itens {
			linhas = append(linhas, item(it))
		}
	}
	if r.Concluida {
		linhas = append(linhas, "", e.fim.Render("✓ Totalização concluída"))
	}
	return strings.Join(linhas, "\n")
}

// linhasCandidatos mostra cartões de duas linhas quando há poucos candidatos (2º turno)
// e uma linha por candidato quando há muitos (1º turno).
func (m *modelo) linhasCandidatos(r *Resultado, largura int) []string {
	e := m.est
	cartao := len(r.Candidatos) <= 4
	larguraUtil := min(largura, 100)
	var linhas []string
	for i, c := range r.Candidatos {
		cor := m.corCandidato(c.Numero)
		delta := ""
		if antes, ok := m.anteriores[c.Numero]; ok && c.Votos != antes {
			if d := c.Votos - antes; d > 0 {
				delta = e.positivo.Render("+" + fmtInt(d))
			} else {
				delta = e.negativo.Render(fmtInt(d))
			}
		}
		pctTexto := e.negrito.Render(alinharDireita(fmtPct2(c.Pct), 7))
		votos := alinharDireita(fmtInt(c.Votos), 11)

		if cartao {
			nome := lipgloss.NewStyle().Bold(true).Foreground(cor).Render(c.Nome)
			esq := fmt.Sprintf(" %dº  %s  %s", i+1, nome, e.fraco.Render(c.Partido+" · "+c.Numero))
			linhas = append(linhas, juntarPontas(esq, m.selo(c.Situacao), larguraUtil))
			fixo := 5 + 2 + 7 + 2 + 11 + 6 + 2 + 12
			linhas = append(linhas,
				"     "+barraVotos(c.Pct, max(larguraUtil-fixo, 5), cor, e.corVazio)+"  "+pctTexto+
					"  "+votos+e.fraco.Render(" votos")+"  "+delta,
				"")
			continue
		}
		nome := lipgloss.NewStyle().Bold(true).Foreground(cor).Render(alinharEsquerda(ansi.Truncate(c.Nome, 26, "…"), 26))
		esq := fmt.Sprintf("%3dº %3s  %s  %s  %s  ", i+1, c.Numero, nome, votos, pctTexto)
		barra := ""
		if w := min(larguraUtil-lipgloss.Width(esq)-14, 30); w >= 4 {
			barra = barraVotos(c.Pct, w, cor, e.corVazio) + "  "
		}
		linhas = append(linhas, esq+barra+m.selo(c.Situacao)+" "+delta)
	}
	return linhas
}

// barraSecoes desenha o andamento da apuração com degradê. Usa blocos cheios: no modo padrão
// de meio-bloco, a barra deixa a cor de fundo ativa e ela vaza para o texto seguinte.
func barraSecoes(pct float64, largura int) string {
	barra := progress.New(progress.WithDefaultBlend(), progress.WithoutPercentage(), progress.WithWidth(largura),
		progress.WithFillCharacters(progress.DefaultFullCharFullBlock, progress.DefaultEmptyCharBlock))
	return barra.ViewAs(pct/100) + ansi.ResetStyle
}

func barraVotos(pct float64, largura int, cor, vazio color.Color) string {
	cheio := min(largura, int(math.Round(pct/100*float64(largura))))
	return lipgloss.NewStyle().Foreground(cor).Render(strings.Repeat("█", cheio)) +
		lipgloss.NewStyle().Foreground(vazio).Render(strings.Repeat("░", largura-cheio))
}

func (m *modelo) selo(situacao string) string {
	switch strings.ToLower(situacao) {
	case "eleito":
		return m.est.eleito.Render("ELEITO")
	case "2º turno":
		return m.est.segundoTurno.Render("2º TURNO")
	case "":
		return ""
	}
	return m.est.naoEleito.Render(strings.ToLower(situacao))
}
