package main

import (
	"cmp"
	"image/color"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
)

const (
	sepGrupo  = " │ "
	sepColuna = "  "
	sepPartes = " " // entre votos e % na mesma coluna
)

var titulosGrupos = [3]string{"1º TURNO", "2º TURNO", "VARIAÇÃO 1º → 2º TURNO"}

// linhasTabela: o totalizador do Brasil em cima e, abaixo, um estado por linha, em ordem
// alfabética. A ordem exibida sai de linhasOrdenadas.
var linhasTabela = append([]string{"br"}, ufs...)

// ordem das linhas da tabela, trocada com a tecla s.
type ordem int

const (
	porUF ordem = iota
	porApuracao
	porCandidato2 // candidato de número maior (Flávio)
	porCandidato1 // candidato de número menor (Lula)
	porAVir       // votos válidos que ainda devem sair
	numOrdens
)

// linhasBase são as linhas da tabela na tela atual, com o totalizador primeiro: o Brasil e os
// estados ou, na tela Cidades, o estado e as suas cidades que passam na busca.
func (m *modelo) linhasBase() []string {
	switch {
	case m.aba != abaCidades:
		return slices.Clone(linhasTabela)
	case m.cidadeBairros != "":
		return append([]string{m.cidadeBairros}, m.bairrosFiltrados()...)
	}
	return append([]string{m.ufCidades}, m.cidadesFiltradas()...)
}

// linhasOrdenadas mantém o totalizador em cima e ordena as demais linhas pelo critério escolhido,
// do maior para o menor. Linhas ainda sem apuração vão para o fim, em ordem alfabética.
func (m *modelo) linhasOrdenadas() []string {
	linhas := m.linhasBase()
	ordem := m.nav().ordem
	if ordem == porUF {
		return linhas
	}
	cands, temCands := m.candidatosSegundoTurno()
	valor := func(uf string) (float64, bool) {
		if ordem == porAVir {
			return m.votosAVir(uf) // vale também para estados que ainda não começaram a apurar
		}
		r := m.dados[alvo{m.turnoOrdem(), uf}]
		if r == nil || r.SecoesTotalizadas == 0 {
			return 0, false
		}
		switch ordem {
		case porApuracao:
			return r.PctSecoes, true
		case porCandidato1, porCandidato2:
			if !temCands {
				return 0, false
			}
			c, _ := r.candidato(cands[m.indiceCandidatoOrdem()].Numero)
			return c.Pct, true
		}
		return 0, false
	}
	slices.SortStableFunc(linhas[1:], func(a, b string) int {
		va, okA := valor(a)
		vb, okB := valor(b)
		switch {
		case okA && !okB:
			return -1
		case !okA && okB:
			return 1
		case !okA:
			return 0 // os dois sem apuração: fica a ordem alfabética
		}
		return cmp.Compare(vb, va)
	})
	return linhas
}

// indiceCandidatoOrdem é a posição, em candidatosSegundoTurno, do candidato usado na ordem.
func (m *modelo) indiceCandidatoOrdem() int {
	if m.nav().ordem == porCandidato2 {
		return 1
	}
	return 0
}

// descricaoOrdem aparece na linha acima da tabela.
func (m *modelo) descricaoOrdem() string {
	switch m.nav().ordem {
	case porApuracao:
		return "por % de urnas apuradas"
	case porAVir:
		return "por votos a vir"
	case porCandidato1, porCandidato2:
		if cands, ok := m.candidatosSegundoTurno(); ok {
			descricao := "por % de " + primeiroNome(cands[m.indiceCandidatoOrdem()].Nome)
			if m.turnoOrdem() == 1 {
				descricao += " no 1º turno"
			}
			return descricao
		}
	}
	if m.aba == abaCidades {
		return "por nome"
	}
	return "por UF"
}

// marcaOrdem põe uma seta na unidade ("%", "mi") da coluna usada na ordem.
func marcaOrdem(ativa bool) string { return marcaUnidade("%", ativa) }

func marcaUnidade(unidade string, ativa bool) string {
	if ativa {
		return unidade + "▼"
	}
	return unidade
}

// votosAVir estima quantos votos válidos ainda vão sair de uma linha: eleitores das seções
// não apuradas × comparecimento × proporção de válidos, com as taxas do 2º turno já apurado na
// UF (ou do 1º turno, enquanto o 2º não começou ali). O BR é a soma das UFs.
func (m *modelo) votosAVir(uf string) (float64, bool) {
	if uf == "br" {
		soma, algum := 0.0, false
		for _, u := range ufs {
			if v, ok := m.votosAVir(u); ok {
				soma, algum = soma+v, true
			}
		}
		return soma, algum
	}
	r2 := m.dados[alvo{2, uf}]
	if r2 == nil {
		return 0, false
	}
	base := r2
	if r2.SecoesTotalizadas == 0 {
		base = m.dados[alvo{1, uf}]
	}
	if base == nil || base.Comparecimento+base.Abstencao == 0 || base.TotalVotos == 0 {
		return 0, false
	}
	resta := float64(r2.Eleitorado - r2.EleitoradoApurado)
	comparecimento := float64(base.Comparecimento) / float64(base.Comparecimento+base.Abstencao)
	validos := float64(base.Validos) / float64(base.TotalVotos)
	return resta * comparecimento * validos, true
}

type celula struct {
	partes   []string // ex.: quantidade e percentual, alinhados cada um na sua sub-coluna
	estilo   lipgloss.Style
	destaque *lipgloss.Style // fundo próprio só para a última parte (o percentual), ou nil
}

type coluna struct {
	grupo        int
	titulo       string
	estiloTitulo lipgloss.Style
	sub          []string
	celulas      []celula // uma por linha, na ordem de linhasOrdenadas
	minimos      []int    // largura mínima de cada parte
	larguras     []int    // de cada parte
	folga        int      // espaços à direita quando o título é mais largo que o conteúdo
	largura      int
	fundoProprio bool // as células têm cor de fundo própria, que a linha selecionada não cobre
	esquerda     bool // texto alinhado à esquerda (coluna de UFs)
}

func (c *coluna) medir() {
	c.larguras = make([]int, len(c.sub))
	for j, s := range c.sub {
		c.larguras[j] = lipgloss.Width(s)
		if j < len(c.minimos) {
			c.larguras[j] = max(c.larguras[j], c.minimos[j])
		}
	}
	for _, cel := range c.celulas {
		if len(cel.partes) == len(c.sub) {
			for j, p := range cel.partes {
				c.larguras[j] = max(c.larguras[j], lipgloss.Width(p))
			}
		}
	}
	c.largura = (len(c.larguras) - 1) * len(sepPartes)
	for _, w := range c.larguras {
		c.largura += w
	}
	// Título mais largo que o conteúdo: o bloco de números fica centralizado sob ele.
	if excesso := lipgloss.Width(c.titulo) - c.largura; excesso > 0 {
		c.folga = excesso / 2
		c.larguras[0] += excesso - c.folga
		c.largura += excesso
	}
}

// texto alinha as partes à direita, cada uma na sua largura. Uma célula com outro número
// de partes (ex.: "—" quando não há dados) fica centralizada na coluna toda, assim como
// as células com fundo próprio, que viram blocos coloridos.
func (c *coluna) texto(partes []string) string {
	if c.esquerda {
		return alinharEsquerda(strings.Join(partes, sepPartes), c.largura)
	}
	if len(partes) != len(c.larguras) || c.fundoProprio {
		return centralizar(strings.Join(partes, sepPartes), c.largura)
	}
	alinhadas := make([]string, len(partes))
	for j, p := range partes {
		alinhadas[j] = alinharDireita(p, c.larguras[j])
	}
	return strings.Join(alinhadas, sepPartes) + strings.Repeat(" ", c.folga)
}

type tabela struct {
	tituloRotulo string // cabeçalho da primeira coluna: " UF" ou " CIDADE"
	rotulos      []string
	largRotulo   int
	cols         []coluna
}

type medida struct {
	qtd int64
	pct float64
}

// medidaDe devolve, na ordem das colunas: candidato 1, candidato 2, brancos + nulos e abstenção.
func medidaDe(r *Resultado, i int, cands [2]Candidato) medida {
	switch i {
	case 0, 1:
		c, _ := r.candidato(cands[i].Numero)
		return medida{c.Votos, c.Pct}
	case 2:
		bn := r.Brancos + r.Nulos
		return medida{bn, pct(bn, r.TotalVotos)}
	default:
		return medida{r.Abstencao, r.PctAbstencao}
	}
}

func rotuloUF(uf string) string {
	switch uf {
	case "br":
		return "BR"
	case "zz":
		return "EXT"
	}
	return strings.ToUpper(uf)
}

// primeiroNome encurta o nome de urna para o cabeçalho: "FLAVIO BOLSONARO" -> "FLAVIO".
func primeiroNome(nome string) string {
	if partes := strings.Fields(nome); len(partes) > 0 {
		return partes[0]
	}
	return nome
}

func (m *modelo) montarTabela() (tabela, bool) {
	cands, ok := m.candidatosSegundoTurno()
	if !ok {
		return tabela{}, false
	}
	e := m.est
	// Cabeçalhos curtos para a tabela caber em menos colunas; a legenda explica as abreviações.
	titulos := [4]string{primeiroNome(cands[0].Nome), primeiroNome(cands[1].Nome), "B+N *", "ABST."}
	estiloTitulo := func(i int) lipgloss.Style {
		if i < 2 {
			return lipgloss.NewStyle().Bold(true).Foreground(e.corCand[i])
		}
		return e.negrito
	}

	// Na tela Cidades, a tabela deixa de fora HORA, A VIR e a repetição dos nomes na variação,
	// para caber com a coluna dos nomes, bem mais larga que a das siglas.
	// Nos bairros sai também APUR., que não se aplica a dados já totalizados.
	cidades, bairros := m.aba == abaCidades, m.modoBairros()
	linhas := m.linhasOrdenadas()
	t := tabela{tituloRotulo: " UF", rotulos: make([]string, len(linhas))}
	switch {
	case bairros:
		t.tituloRotulo, t.largRotulo = " BAIRRO", m.larguraRotulosCidades()
	case cidades:
		t.tituloRotulo, t.largRotulo = " CIDADE", m.larguraRotulosCidades()
	}
	for k, uf := range linhas {
		t.rotulos[k] = " " + m.rotuloLinha(uf)
		t.largRotulo = max(t.largRotulo, lipgloss.Width(t.rotulos[k]))
	}

	// Larguras de votos e % do 1º turno, medidas mesmo com os votos ocultos: as colunas do
	// 2º turno partem delas para a tabela não mudar de largura quando a apuração começa.
	var larguras1 [4][2]int
	for turno := 1; turno <= 2; turno++ {
		switch {
		case turno == 2 && bairros:
		case turno == 2 && cidades:
			t.cols = append(t.cols, m.colunaApuradas(linhas))
		case turno == 2:
			t.cols = append(t.cols, m.colunaHora(linhas), m.colunaApuradas(linhas), m.colunaAVir(linhas))
		}
		mostrarVotos := turno == 2 || m.votosTurno1
		for i := range titulos {
			c := coluna{grupo: turno - 1, titulo: titulos[i], estiloTitulo: estiloTitulo(i), sub: []string{"%"}}
			if mostrarVotos {
				c.sub = []string{m.rotuloVotos(), "%"}
			}
			if ordem := m.nav().ordem; turno == m.turnoOrdem() && i < 2 && (ordem == porCandidato1 || ordem == porCandidato2) && i == m.indiceCandidatoOrdem() {
				c.sub[len(c.sub)-1] = marcaOrdem(true)
			}
			if turno == 2 {
				c.minimos = larguras1[i][:]
			}
			for _, uf := range linhas {
				r := m.dados[alvo{turno, uf}]
				if r == nil || r.SecoesTotalizadas == 0 {
					c.celulas = append(c.celulas, celula{partes: []string{"—"}, estilo: e.fraco})
					continue
				}
				md := medidaDe(r, i, cands)
				// Com a tecla m, os votos aparecem em milhares (o arredondamento é só na exibição).
				votos, porcentagem := m.fmtVotos(md.qtd), fmtDecimal(md.pct)
				if turno == 1 {
					larguras1[i][0] = max(larguras1[i][0], lipgloss.Width(votos))
					larguras1[i][1] = max(larguras1[i][1], lipgloss.Width(porcentagem))
				}
				partes := []string{porcentagem}
				if mostrarVotos {
					partes = []string{votos, porcentagem}
				}
				cel := celula{partes: partes, estilo: e.celula}
				if turno == 2 && i < 2 {
					cel.destaque = m.destaqueLideranca(r, i, cands)
				}
				c.celulas = append(c.celulas, cel)
			}
			c.medir()
			t.cols = append(t.cols, c)
		}
	}

	if !cidades {
		t.cols = append(t.cols, m.colunaUF(linhas))
	}
	// A variação compara os percentuais, que já fazem sentido durante a apuração: diferença em
	// pontos percentuais ou, com a tecla v, variação relativa do percentual.
	sub := "pontos"
	if m.variacaoRelativa {
		sub = "%"
	}
	for i := range titulos {
		c := coluna{grupo: 2, titulo: titulos[i], estiloTitulo: estiloTitulo(i), sub: []string{sub}}
		for _, uf := range linhas {
			r1, r2 := m.dados[alvo{1, uf}], m.dados[alvo{2, uf}]
			if r1 == nil || r2 == nil || r2.SecoesTotalizadas == 0 {
				c.celulas = append(c.celulas, celula{partes: []string{"—"}, estilo: e.fraco})
				continue
			}
			antes, depois := medidaDe(r1, i, cands), medidaDe(r2, i, cands)
			v := depois.pct - antes.pct
			if m.variacaoRelativa {
				if antes.pct == 0 {
					c.celulas = append(c.celulas, celula{partes: []string{"—"}, estilo: e.fraco})
					continue
				}
				v = (depois.pct/antes.pct - 1) * 100
			}
			estilo := e.celula
			switch {
			case i < 2 && v > 0:
				estilo = e.positivo
			case i < 2 && v < 0:
				estilo = e.negativo
			}
			c.celulas = append(c.celulas, celula{partes: []string{fmtVariacao(v, "")}, estilo: estilo})
		}
		c.medir()
		t.cols = append(t.cols, c)
	}
	return t, true
}

// destaqueLideranca pinta o percentual de verde para quem está na frente na linha e de
// vermelho para quem está atrás; empate fica sem cor.
func (m *modelo) destaqueLideranca(r *Resultado, i int, cands [2]Candidato) *lipgloss.Style {
	eu, outro := medidaDe(r, i, cands).qtd, medidaDe(r, 1-i, cands).qtd
	switch {
	case eu > outro:
		return &m.est.ganhando
	case eu < outro:
		return &m.est.perdendo
	}
	return nil
}

// colunaUF repete as siglas no começo do grupo da variação, para a leitura no lado direito da tela.
func (m *modelo) colunaUF(linhas []string) coluna {
	c := coluna{grupo: 2, titulo: "UF", estiloTitulo: m.est.cabecalho, sub: []string{""}, esquerda: true}
	for _, uf := range linhas {
		c.celulas = append(c.celulas, celula{partes: []string{rotuloUF(uf)}, estilo: m.est.rotulo})
	}
	c.medir()
	return c
}

// colunaHora mostra a hora (hh:mm) em que o TSE publicou pela última vez o arquivo do 2º turno
// de cada linha; ajuda a ver quando um estado está com os dados parados.
func (m *modelo) colunaHora(linhas []string) coluna {
	c := coluna{grupo: 1, titulo: "HORA", estiloTitulo: m.est.negrito, sub: []string{""}}
	for _, uf := range linhas {
		r := m.dados[alvo{2, uf}]
		if r == nil || r.SecoesTotalizadas == 0 || len(r.Hora) < 5 {
			c.celulas = append(c.celulas, celula{partes: []string{"—"}, estilo: m.est.fraco})
			continue
		}
		c.celulas = append(c.celulas, celula{partes: []string{r.Hora[:5]}, estilo: m.est.celula})
	}
	c.medir()
	return c
}

// colunaAVir mostra, em milhões, a estimativa de votos válidos que ainda devem sair de cada linha.
func (m *modelo) colunaAVir(linhas []string) coluna {
	c := coluna{grupo: 1, titulo: "A VIR", estiloTitulo: m.est.negrito, sub: []string{marcaUnidade("mi", m.nav().ordem == porAVir)}}
	for _, uf := range linhas {
		v, ok := m.votosAVir(uf)
		if !ok {
			c.celulas = append(c.celulas, celula{partes: []string{"—"}, estilo: m.est.fraco})
			continue
		}
		c.celulas = append(c.celulas, celula{partes: []string{fmtCasas(v/1e6, 1)}, estilo: m.est.celula})
	}
	c.medir()
	return c
}

// colunaApuradas abre o grupo do 2º turno com o % de urnas apuradas em cada linha, sobre um
// fundo que vai do vermelho (0%) ao verde (100%).
func (m *modelo) colunaApuradas(linhas []string) coluna {
	c := coluna{grupo: 1, titulo: "APUR.", estiloTitulo: m.est.negrito, sub: []string{marcaOrdem(m.nav().ordem == porApuracao)}, fundoProprio: true}
	for _, uf := range linhas {
		r := m.dados[alvo{2, uf}]
		if r == nil {
			c.celulas = append(c.celulas, celula{partes: []string{"—"}, estilo: m.est.fraco})
			continue
		}
		fundo, texto := corApuracao(r.PctSecoes)
		c.celulas = append(c.celulas, celula{partes: []string{fmtDecimal(r.PctSecoes)},
			estilo: lipgloss.NewStyle().Bold(true).Foreground(texto).Background(fundo)})
	}
	c.medir()
	return c
}

// corApuracao gira o matiz de 0° (vermelho) a 120° (verde), passando por laranja e amarelo,
// e escolhe texto preto ou branco conforme a luminância do fundo.
func corApuracao(pct float64) (fundo, texto color.Color) {
	c := colorful.Hsl(120*min(max(pct/100, 0), 1), 0.7, 0.48)
	r, g, b := c.LinearRgb()
	if 0.2126*r+0.7152*g+0.0722*b > 0.179 {
		return c, lipgloss.Color("#111827")
	}
	return c, lipgloss.Color("#FFFFFF")
}

func (t tabela) separadorAntes(vis []int, k int) string {
	if k == 0 || t.cols[vis[k-1]].grupo != t.cols[vis[k]].grupo {
		return sepGrupo
	}
	return sepColuna
}

// visiveis devolve as colunas, a partir de inicio, que cabem em largura ao lado da coluna fixa.
func (t tabela) visiveis(largura, inicio int) []int {
	usado := t.largRotulo
	var vis []int
	for j := inicio; j < len(t.cols); j++ {
		sep := sepColuna
		if len(vis) == 0 || t.cols[vis[len(vis)-1]].grupo != t.cols[j].grupo {
			sep = sepGrupo
		}
		w := lipgloss.Width(sep) + t.cols[j].largura
		if len(vis) > 0 && usado+w > largura {
			break
		}
		vis = append(vis, j)
		usado += w
	}
	return vis
}

// maxInicio é o menor deslocamento a partir do qual a última coluna já aparece.
func (t tabela) maxInicio(largura int) int {
	for inicio := range t.cols {
		if vis := t.visiveis(largura, inicio); vis[len(vis)-1] == len(t.cols)-1 {
			return inicio
		}
	}
	return 0
}

func (t tabela) linha(vis []int, sep lipgloss.Style, rotulo string, conteudo func(c *coluna) string) string {
	var b strings.Builder
	b.WriteString(rotulo)
	for k, j := range vis {
		b.WriteString(sep.Render(t.separadorAntes(vis, k)))
		b.WriteString(conteudo(&t.cols[j]))
	}
	return b.String()
}

func (t tabela) regua(vis []int, e estilos) string {
	var b strings.Builder
	b.WriteString(strings.Repeat("─", t.largRotulo))
	for k, j := range vis {
		if t.separadorAntes(vis, k) == sepGrupo {
			b.WriteString("─┼─")
		} else {
			b.WriteString("──")
		}
		b.WriteString(strings.Repeat("─", t.cols[j].largura))
	}
	return e.separador.Render(b.String())
}

func (t tabela) linhaDados(i int, vis []int, e estilos, selecionada bool) string {
	fundo := func(s lipgloss.Style) lipgloss.Style {
		if i == 0 {
			s = s.Bold(true).Background(e.corTotal)
		}
		if selecionada {
			s = s.Background(e.corSelecao)
		}
		return s
	}
	rotulo := fundo(e.rotulo).Render(alinharEsquerda(t.rotulos[i], t.largRotulo))
	return t.linha(vis, fundo(e.separador), rotulo, func(c *coluna) string {
		cel := c.celulas[i]
		if cel.destaque != nil && len(cel.partes) == len(c.larguras) {
			// Só o percentual ganha o fundo de liderança; os votos seguem o fundo da linha.
			base, n := fundo(cel.estilo), len(cel.partes)-1
			var b strings.Builder
			for j, p := range cel.partes[:n] {
				b.WriteString(base.Render(alinharDireita(p, c.larguras[j]) + sepPartes))
			}
			b.WriteString(cel.destaque.Bold(i == 0).Render(alinharDireita(cel.partes[n], c.larguras[n])))
			if c.folga > 0 {
				b.WriteString(base.Render(strings.Repeat(" ", c.folga)))
			}
			return b.String()
		}
		estilo := fundo(cel.estilo)
		if c.fundoProprio {
			estilo = cel.estilo.Bold(true)
		}
		return estilo.Render(c.texto(cel.partes))
	})
}

// render desenha o cabeçalho e o totalizador fixos e, abaixo, as linhas dos estados a partir
// de inicioLinha. cursor é o índice da linha selecionada (-1 para nenhuma).
func (t tabela) render(e estilos, largura, altura, inicioCol, inicioLinha, cursor int) string {
	vis := t.visiveis(largura, inicioCol)

	var grupos strings.Builder
	grupos.WriteString(strings.Repeat(" ", t.largRotulo))
	for k := 0; k < len(vis); {
		g := t.cols[vis[k]].grupo
		w, fim := t.cols[vis[k]].largura, k+1
		for fim < len(vis) && t.cols[vis[fim]].grupo == g {
			w += len(sepColuna) + t.cols[vis[fim]].largura
			fim++
		}
		grupos.WriteString(e.separador.Render(sepGrupo))
		grupos.WriteString(e.grupo[g].Render(centralizar(titulosGrupos[g], w)))
		k = fim
	}

	linhas := []string{
		grupos.String(),
		t.linha(vis, e.separador, e.cabecalho.Render(alinharEsquerda(t.tituloRotulo, t.largRotulo)), func(c *coluna) string {
			if c.esquerda {
				return c.estiloTitulo.Render(alinharEsquerda(c.titulo, c.largura))
			}
			return c.estiloTitulo.Render(centralizar(c.titulo, c.largura))
		}),
		t.linha(vis, e.separador, strings.Repeat(" ", t.largRotulo), func(c *coluna) string {
			return e.fraco.Render(c.texto(c.sub))
		}),
		t.regua(vis, e),
		t.linhaDados(0, vis, e, cursor == 0),
		t.regua(vis, e),
	}
	n := max(1, altura-len(linhas))
	for i := 1 + inicioLinha; i < len(t.rotulos) && i < 1+inicioLinha+n; i++ {
		linhas = append(linhas, t.linhaDados(i, vis, e, cursor == i))
	}
	return strings.Join(linhas, "\n")
}

// linhasFixasTabela é quantas linhas o cabeçalho e o totalizador ocupam.
const linhasFixasTabela = 6

// fmtVotos mostra a quantidade inteira ou, com a tecla m, em milhares com uma casa.
func (m *modelo) fmtVotos(n int64) string {
	if m.votosMil {
		return fmtMil(n)
	}
	return fmtInt(n)
}

func (m *modelo) rotuloVotos() string {
	if m.votosMil {
		return "(x 1000)"
	}
	return "votos"
}
