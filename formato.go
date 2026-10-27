package main

import (
	"math"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func numero(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

// decimal lê os percentuais do TSE, que vêm com vírgula ("47,027772356").
func decimal(s string) float64 {
	f, _ := strconv.ParseFloat(strings.Replace(strings.TrimSpace(s), ",", ".", 1), 64)
	return f
}

func pct(parte, total int64) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(parte) / float64(total)
}

// fmtInt formata no padrão brasileiro: 53879538 -> "53.879.538".
func fmtInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	inicio := 0
	if n < 0 {
		inicio = 1
	}
	for i := len(s) - 3; i > inicio; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	return s
}

// fmtMil mostra uma quantidade em milhares com uma casa: 53879538 -> "53.879,5".
func fmtMil(n int64) string {
	decimos := int64(math.Round(float64(n) / 100))
	inteiro, resto := decimos/10, decimos%10
	if resto < 0 {
		resto = -resto
	}
	return fmtInt(inteiro) + "," + strconv.FormatInt(resto, 10)
}

// fmtDecimal mostra os percentuais com uma casa, arredondando a metade para cima: 81,44 -> "81,4",
// 0,25 -> "0,3".
func fmtDecimal(f float64) string { return fmtCasas(f, 1) }

// fmtCasas arredonda f para o número de casas pedido (metade para cima) e usa vírgula.
func fmtCasas(f float64, casas int) string {
	escala := math.Pow10(casas)
	f = math.Round(f*escala) / escala
	if f == 0 {
		f = 0 // troca -0 por 0, evitando "-0,0"
	}
	return strings.Replace(strconv.FormatFloat(f, 'f', casas, 64), ".", ",", 1)
}

// fmtPct2 é o percentual com duas casas, usado no painel: 50,1049 -> "50,10%".
func fmtPct2(f float64) string { return fmtCasas(f, 2) + "%" }

func fmtPct(f float64) string { return fmtDecimal(f) + "%" }

// fmtVariacao sempre mostra o sinal: "+8,7", "-1,2", "+0,0".
func fmtVariacao(f float64, sufixo string) string {
	s := fmtDecimal(f)
	if !strings.HasPrefix(s, "-") {
		s = "+" + s
	}
	return s + sufixo
}

// As funções de alinhamento medem a largura visível, ignorando códigos ANSI.

func alinharDireita(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

func alinharEsquerda(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func centralizar(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	d := w - lipgloss.Width(s)
	if d <= 0 {
		return s
	}
	return strings.Repeat(" ", d/2) + s + strings.Repeat(" ", d-d/2)
}

// juntarPontas põe esq no começo e dir no fim de uma linha de largura w.
func juntarPontas(esq, dir string, w int) string {
	return esq + strings.Repeat(" ", max(1, w-lipgloss.Width(esq)-lipgloss.Width(dir))) + dir
}

// cortarLinhas garante que nenhuma linha passe de w colunas, para o terminal não quebrá-las.
func cortarLinhas(s string, w int) string {
	linhas := strings.Split(s, "\n")
	for i, l := range linhas {
		linhas[i] = ansi.Truncate(l, w, "…")
	}
	return strings.Join(linhas, "\n")
}
