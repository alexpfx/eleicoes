package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	// Poucas consultas simultâneas, por educação com o TSE, que nos picos de acesso recusa
	// consultas com 429 (excesso de consultas).
	consultasCidades  = 4
	loteCidades       = 96 // arquivos por mensagem: a tabela vai se preenchendo durante a carga
	pausaPor429       = 30 * time.Second
	larguraNomeCidade = 22 // nomes maiores são encurtados com "…" na tabela
)

// municipio é uma cidade da lista de municípios do TSE (mun-e<eleição>-cm.json).
type municipio struct {
	chave string // UF + código do município no TSE: "sp71072"
	uf    string
	nome  string // "São José dos Campos"
	busca string // o nome como a busca compara: "SAO JOSE DOS CAMPOS"
}

type listaMunicipios struct {
	porUF    map[string][]municipio // em ordem alfabética
	porChave map[string]municipio
}

type msgMunicipios struct {
	lista *listaMunicipios
	err   error
}

func (c *Cliente) listarMunicipios(ctx context.Context, ciclo string, eleicao int) (*listaMunicipios, error) {
	var cm struct {
		Abr []struct {
			Cd string `json:"cd"`
			Mu []struct {
				Cd string `json:"cd"`
				Nm string `json:"nm"`
			} `json:"mu"`
		} `json:"abr"`
	}
	url := fmt.Sprintf("%s/%s/%d/config/mun-e%06d-cm.json", c.base, ciclo, eleicao, eleicao)
	if _, _, err := c.get(ctx, url, "", &cm); err != nil {
		return nil, err
	}
	l := &listaMunicipios{porUF: map[string][]municipio{}, porChave: map[string]municipio{}}
	for _, a := range cm.Abr {
		uf := strings.ToLower(a.Cd)
		for _, mu := range a.Mu {
			nome := nomeProprio(mu.Nm)
			cidade := municipio{chave: uf + mu.Cd, uf: uf, nome: nome, busca: paraBusca(nome)}
			l.porUF[uf] = append(l.porUF[uf], cidade)
			l.porChave[cidade.chave] = cidade
		}
	}
	for _, cidades := range l.porUF {
		slices.SortFunc(cidades, func(a, b municipio) int { return strings.Compare(a.busca, b.busca) })
	}
	return l, nil
}

// palavrasMinusculas ficam em minúsculas no meio dos nomes: "Santana do Livramento".
var palavrasMinusculas = map[string]bool{"da": true, "das": true, "de": true, "del": true, "do": true, "dos": true, "e": true}

// nomeProprio passa os nomes do TSE, todos em maiúsculas, para a grafia usual:
// "SÃO JOSÉ DOS CAMPOS" -> "São José dos Campos", "OLHO-D'ÁGUA DAS FLORES" -> "Olho-d'Água das Flores".
func nomeProprio(s string) string {
	palavras := strings.Fields(strings.ToLower(s))
	for i, palavra := range palavras {
		partes := strings.Split(palavra, "-")
		for j, parte := range partes {
			inicio := i == 0 && j == 0
			switch resto, apostrofo := strings.CutPrefix(parte, "d'"); {
			case !inicio && palavrasMinusculas[parte]:
			case apostrofo && inicio:
				partes[j] = "D'" + maiuscula(resto)
			case apostrofo:
				partes[j] = "d'" + maiuscula(resto)
			case !inicio && strings.Trim(parte, "ivx") == "":
				partes[j] = strings.ToUpper(parte) // algarismos romanos: "Dom Pedro II"
			case strings.HasPrefix(parte, "("):
				partes[j] = "(" + maiuscula(parte[1:]) // bairros: "Mantiqueira (Venda Nova)"
			default:
				partes[j] = maiuscula(parte)
			}
		}
		palavras[i] = strings.Join(partes, "-")
	}
	return strings.Join(palavras, " ")
}

func maiuscula(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// paraBusca tira acentos, hífens e apóstrofos e passa para maiúsculas, para a busca achar
// "São João d'Aliança" quando se digita "sao joao dal".
func paraBusca(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r = unicode.ToUpper(r); r {
		case 'Á', 'À', 'Â', 'Ã', 'Ä':
			return 'A'
		case 'É', 'È', 'Ê', 'Ë':
			return 'E'
		case 'Í', 'Ì', 'Î', 'Ï':
			return 'I'
		case 'Ó', 'Ò', 'Ô', 'Õ', 'Ö':
			return 'O'
		case 'Ú', 'Ù', 'Û', 'Ü':
			return 'U'
		case 'Ç':
			return 'C'
		case 'Ñ':
			return 'N'
		case '\'', '’':
			return -1
		case '-':
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func ehCidade(chave string) bool { return len(chave) > 2 }

// nomeAbrangencia é o nome por extenso de "br", de uma UF, de uma cidade ("Campinas (SP)") ou de
// um bairro ("Cambuí · Campinas (SP)").
func (m *modelo) nomeAbrangencia(chave string) string {
	if nome, ok := nomesUF[chave]; ok {
		return nome
	}
	if ehBairro(chave) {
		cidade, _, _ := strings.Cut(chave, "#")
		return m.nomeBairro(chave) + " · " + m.nomeAbrangencia(cidade)
	}
	if m.municipios != nil {
		if mu, ok := m.municipios.porChave[chave]; ok {
			return mu.nome + " (" + rotuloUF(mu.uf) + ")"
		}
	}
	return strings.ToUpper(chave)
}

// carga acompanha o download das cidades de um estado.
type carga struct {
	versao        string // idg do arquivo do estado no 2º turno quando a carga começou
	total, feitos int
	pendentes     []item
	ativa         bool
	refazer       bool // baixar de novo mesmo sem mudança no estado (tecla r ou falha na carga anterior)
	falhou        bool // terminou com falhas; a próxima rodada marca refazer
	falhas        int
	naoPublicados int
	primeiraFalha error
}

type msgLoteCidades struct {
	uf    string
	itens []item
	resto []item
}

// ufCidadesEmUso é o estado cujas cidades estão na tela: o da tela Cidades ou o da cidade aberta
// no painel.
func (m *modelo) ufCidadesEmUso() (string, bool) {
	switch {
	case m.aba == abaCidades:
		return m.ufCidades, true
	case m.aba == abaPainel && ehCidade(m.ufPainel):
		return m.ufPainel[:2], true
	}
	return "", false
}

// carregarCidades começa ou retoma, se for preciso, o download das cidades do estado em uso.
// Só uma carga anda por vez: ao trocar de estado, a anterior para no fim do lote e continua
// quando aquele estado volta para a tela. Depois de completa, a carga só é refeita quando o
// arquivo do estado muda (o que já foi baixado volta como 304, sem corpo).
func (m *modelo) carregarCidades() tea.Cmd {
	uf, ok := m.ufCidadesEmUso()
	if !ok || m.eleicoes == nil || time.Now().Before(m.pausaCidades) {
		return nil
	}
	if m.municipios == nil {
		if m.carregandoMunicipios || m.erroMunicipios != "" {
			return nil
		}
		return m.listarMunicipios()
	}
	for _, c := range m.cargas {
		if c.ativa {
			return nil
		}
	}
	versao := ""
	if r := m.dados[alvo{2, uf}]; r != nil {
		versao = r.Idg
	}
	if c := m.cargas[uf]; c != nil && c.versao == versao && !c.refazer {
		if len(c.pendentes) == 0 {
			return nil
		}
		c.ativa = true
		return m.baixarLote(uf, c.pendentes)
	}
	var itens []item
	for _, turno := range []int{2, 1} { // o 2º turno primeiro, para a tabela aparecer antes
		if m.eleicoes.Codigo[turno] == 0 {
			continue
		}
		for _, mu := range m.municipios.porUF[uf] {
			a := alvo{turno, mu.chave}
			if r := m.dados[a]; r != nil && r.Concluida {
				continue
			}
			url := m.cli.urlResultado(m.cfg.ciclo, m.eleicoes.Codigo[turno], mu.chave)
			itens = append(itens, item{alvo: a, url: url, etag: m.etags[url]})
		}
	}
	m.cargas[uf] = &carga{versao: versao, total: len(itens), pendentes: itens, ativa: len(itens) > 0}
	if len(itens) == 0 {
		return nil
	}
	return m.baixarLote(uf, itens)
}

func (m *modelo) listarMunicipios() tea.Cmd {
	m.carregandoMunicipios = true
	cli, ciclo := m.cli, m.cfg.ciclo
	eleicao := cmp.Or(m.eleicoes.Codigo[2], m.eleicoes.Codigo[1])
	return func() tea.Msg {
		ctx, cancelar := context.WithTimeout(context.Background(), time.Minute)
		defer cancelar()
		lista, err := cli.listarMunicipios(ctx, ciclo, eleicao)
		return msgMunicipios{lista, err}
	}
}

func (m *modelo) aplicarMunicipios(msg msgMunicipios) {
	m.carregandoMunicipios = false
	if msg.err != nil {
		fonte := "do TSE"
		if m.simulacao() {
			fonte = "do servidor de simulação"
		}
		m.erroMunicipios = fmt.Sprintf("não foi possível ler a lista de cidades %s (%s)", fonte, resumoErro(msg.err))
		return
	}
	m.municipios, m.erroMunicipios = msg.lista, ""
}

func (m *modelo) baixarLote(uf string, pendentes []item) tea.Cmd {
	cli := m.cli
	return func() tea.Msg {
		n := min(loteCidades, len(pendentes))
		lote := slices.Clone(pendentes[:n])
		ctx, cancelar := context.WithTimeout(context.Background(), time.Minute)
		defer cancelar()
		cli.baixar(ctx, lote, consultasCidades)
		return msgLoteCidades{uf: uf, itens: lote, resto: pendentes[n:]}
	}
}

// aplicarLote guarda um lote de cidades e pede o próximo, se o estado ainda estiver na tela.
func (m *modelo) aplicarLote(msg msgLoteCidades) tea.Cmd {
	c := m.cargas[msg.uf]
	// Os arquivos recusados com 429 voltam para a fila, e a carga pausa.
	recusados := slices.DeleteFunc(slices.Clone(msg.itens), func(it item) bool { return !limitado(it.err) })
	feitos := slices.DeleteFunc(slices.Clone(msg.itens), func(it item) bool { return limitado(it.err) })
	naoPublicados, falhas, primeiraFalha := m.guardar(feitos)
	c.feitos += len(feitos)
	c.pendentes = append(recusados, msg.resto...)
	c.naoPublicados += naoPublicados
	c.falhas += falhas
	c.primeiraFalha = cmp.Or(c.primeiraFalha, primeiraFalha)
	switch {
	case len(recusados) > 0:
		m.pausarCidades()
		c.ativa = false
		return nil
	case len(c.pendentes) > 0:
		if uf, ok := m.ufCidadesEmUso(); ok && uf == msg.uf {
			return m.baixarLote(msg.uf, c.pendentes)
		}
	default:
		var avisos []string
		if c.falhas > 0 {
			avisos = append(avisos, fmt.Sprintf("%d de %d consultas de cidades de %s falharam (%s); nova tentativa na próxima rodada",
				c.falhas, c.total, rotuloUF(msg.uf), resumoErro(c.primeiraFalha)))
			c.falhou = true // refeita na próxima rodada, e não já, para não insistir sem parar
		}
		if c.naoPublicados > 0 {
			avisos = append(avisos, fmt.Sprintf("%d arquivos de cidades de %s ainda não publicados", c.naoPublicados, rotuloUF(msg.uf)))
		}
		m.avisoCidades = strings.Join(avisos, " · ")
	}
	c.ativa = false
	return m.carregarCidades()
}

// pausarCidades suspende o download das cidades quando o TSE segue respondendo 429 (excesso de
// consultas) mesmo depois das novas tentativas: num pico de acesso, as consultas do painel e da
// tabela por estado ficam com a vez.
func (m *modelo) pausarCidades() {
	m.pausaCidades = time.Now().Add(pausaPor429)
	m.avisoCidades = "o TSE está recusando consultas por excesso de acessos (HTTP 429); o download das cidades continua às " +
		m.pausaCidades.Format("15:04:05")
}

// recarregarCidades faz a tecla r baixar de novo as cidades do estado na tela.
func (m *modelo) recarregarCidades() {
	m.erroMunicipios = ""
	if c := m.cargas[m.ufCidades]; c != nil && !c.ativa {
		c.refazer = true
	}
}

// mudarEstadoCidades troca o estado da tela Cidades. A ordem escolhida continua; a busca é
// apagada, porque o nome buscado num estado raramente existe no outro.
func (m *modelo) mudarEstadoCidades(uf string) {
	m.ufCidades, m.cidadeBairros = uf, ""
	m.navCidades = navegacao{ordem: m.navCidades.ordem}
	m.busca.Reset()
	m.busca.Blur()
}

func (m *modelo) trocarEstado(d int) {
	i := slices.Index(ufs, m.ufCidades)
	m.mudarEstadoCidades(ufs[(i+d+len(ufs))%len(ufs)])
}

// cidadesFiltradas são as cidades do estado cujo nome contém o texto da busca.
func (m *modelo) cidadesFiltradas() []string {
	if m.municipios == nil {
		return nil
	}
	busca := paraBusca(m.busca.Value())
	var chaves []string
	for _, mu := range m.municipios.porUF[m.ufCidades] {
		if strings.Contains(mu.busca, busca) {
			chaves = append(chaves, mu.chave)
		}
	}
	return chaves
}

// rotuloLinha é o texto da primeira coluna: a sigla da UF ou o nome da cidade, encurtado. Na tela
// Cidades, a linha do estado é o total.
func (m *modelo) rotuloLinha(chave string) string {
	switch {
	case m.modoBairros():
		return m.rotuloBairro(chave)
	case ehCidade(chave):
		if mu, ok := m.municipios.porChave[chave]; ok {
			return ansi.Truncate(mu.nome, larguraNomeCidade, "…")
		}
		return chave
	case m.aba == abaCidades:
		return "Total " + rotuloUF(chave)
	}
	return rotuloUF(chave)
}

// larguraRotulosCidades mede os nomes de todas as cidades do estado, e não só os da busca, para a
// tabela não mudar de largura enquanto se digita.
func (m *modelo) larguraRotulosCidades() int {
	if m.modoBairros() {
		w := lipgloss.Width(" " + m.rotuloLinha(m.cidadeBairros))
		if lista := m.bairros[m.cidadeBairros[:2]]; lista != nil {
			for _, b := range lista.porCidade[m.cidadeBairros] {
				w = max(w, 1+lipgloss.Width(m.rotuloLinha(b.chave)))
			}
		}
		return w
	}
	w := lipgloss.Width(" " + m.rotuloLinha(m.ufCidades))
	if m.municipios != nil {
		for _, mu := range m.municipios.porUF[m.ufCidades] {
			w = max(w, 1+lipgloss.Width(m.rotuloLinha(mu.chave)))
		}
	}
	return w
}

// teclaBusca trata as teclas com o campo de busca ativo: vão para o texto, menos as que saem do
// campo e as setas, que movem a seleção entre as cidades (ou bairros) encontradas.
func (m *modelo) teclaBusca(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return tea.Quit
	case "esc":
		m.limparBusca()
	case "enter":
		m.busca.Blur()
	case "up":
		m.mover(-1)
	case "down":
		m.mover(1)
	case "pgup":
		m.mover(-m.linhasVisiveis())
	case "pgdown":
		m.mover(m.linhasVisiveis())
	default:
		antes := m.busca.Value()
		var cmd tea.Cmd
		m.busca, cmd = m.busca.Update(msg)
		if m.busca.Value() != antes {
			m.nav().cursor, m.nav().linhaInicio = 1, 0 // a primeira cidade ou bairro encontrado
		}
		return cmd
	}
	return nil
}

// limparBusca apaga a busca e mantém selecionada a linha que estava selecionada.
func (m *modelo) limparBusca() {
	nav := m.nav()
	selecionada := m.linhasOrdenadas()[min(nav.cursor, len(m.linhasOrdenadas())-1)]
	m.busca.Reset()
	m.busca.Blur()
	nav.cursor = max(0, slices.Index(m.linhasOrdenadas(), selecionada))
}

func novaBusca(escuro bool) textinput.Model {
	b := textinput.New()
	b.Prompt = "buscar: "
	b.Placeholder = "nome da cidade"
	b.SetWidth(24)
	estilos := textinput.DefaultStyles(escuro)
	estilos.Cursor.Blink = false // cursor fixo, sem precisar de mensagens de piscar
	b.SetStyles(estilos)
	return b
}

// renderAbasEstados é a linha de abas da tela Cidades, uma por estado, com o atual em destaque.
// Em telas estreitas as abas perdem o espaçamento, para o estado atual não ficar de fora.
func (m *modelo) renderAbasEstados() string {
	espaco := 1
	if len(ufs)*4+1 > m.largura {
		espaco = 0
	}
	abas := make([]string, len(ufs))
	for i, uf := range ufs {
		estilo := m.est.abaEstado
		if uf == m.ufCidades {
			estilo = m.est.abaEstadoAtiva
		}
		abas[i] = estilo.Padding(0, espaco).Render(rotuloUF(uf))
	}
	return strings.Join(abas, strings.Repeat(" ", 1-espaco))
}

// resumoCidades vai acima da tabela de cidades: o estado, quantas cidades aparecem, a ordem, o
// andamento do download e, à direita, a busca.
func (m *modelo) resumoCidades() string {
	e := m.est
	esq := " " + e.negrito.Render(nomesUF[m.ufCidades])
	if m.modoBairros() {
		esq = m.resumoBairros()
	} else if m.municipios != nil {
		total := len(m.municipios.porUF[m.ufCidades])
		qtd := fmtInt(int64(total)) + " cidades"
		if m.busca.Value() != "" {
			qtd = fmt.Sprintf("%s de %s cidades", fmtInt(int64(len(m.cidadesFiltradas()))), fmtInt(int64(total)))
		}
		esq += e.fraco.Render(" · " + qtd + " · ordem: " + m.descricaoOrdem())
	}
	// Nos bairros, o andamento também aparece: a linha do total é a cidade, que vem nessa carga.
	if m.municipios != nil {
		switch c := m.cargas[m.ufCidades]; {
		case c == nil && time.Now().Before(m.pausaCidades):
			esq += "   " + e.fraco.Render("download pausado pelo TSE até "+m.pausaCidades.Format("15:04:05"))
		case c == nil:
			// Ainda na fila: outra carga termina o lote antes desta começar.
			esq += "   " + barraSecoes(0, 12) + e.fraco.Render(" baixando os arquivos do TSE…")
		case len(c.pendentes) > 0 && c.total > 0:
			estado := "baixando"
			if time.Now().Before(m.pausaCidades) {
				estado = "pausado pelo TSE: " + m.pausaCidades.Format("15:04:05") + " ·"
			}
			esq += "   " + barraSecoes(pct(int64(c.feitos), int64(c.total)), 12) +
				e.fraco.Render(fmt.Sprintf(" %s %s de %s arquivos do TSE", estado, fmtInt(int64(c.feitos)), fmtInt(int64(c.total))))
		}
	}
	var dir string
	switch {
	case m.busca.Focused():
		dir = m.busca.View() + " "
	case m.busca.Value() != "":
		dir = e.fraco.Render("busca: ") + e.negrito.Render(m.busca.Value()) + e.fraco.Render(" · esc limpa ")
	case !m.telaLimpa() && m.modoBairros():
		dir = e.fraco.Render("/ buscar bairro · esc volta ")
	case !m.telaLimpa():
		dir = e.fraco.Render("/ buscar cidade · b bairros ")
	}
	return juntarPontas(esq, dir, m.largura)
}

// corpoCidades é a tela Cidades: abas dos estados, resumo, tabela e legenda.
func (m *modelo) corpoCidades(altura int) string {
	topo := m.renderAbasEstados() + "\n" + m.resumoCidades()
	if m.modoBairros() {
		return m.corpoBairros(topo, altura)
	}
	t, ok := m.montarTabela()
	switch {
	case m.erroMunicipios != "":
		return topo + "\n\n" + m.est.aviso.Width(m.largura).Render(" "+m.erroMunicipios+" · r tenta de novo")
	case m.municipios == nil:
		return topo + "\n\n" + m.est.fraco.Render(" Carregando a lista de cidades…")
	case !ok:
		return topo + "\n\n" + m.est.fraco.Render(" Aguardando os dados dos candidatos do 2º turno…")
	}
	cursor := m.navCidades.cursor
	if m.telaLimpa() {
		cursor = -1
	}
	legenda := m.legendaRenderizada()
	tabela := t.render(m.est, m.largura, altura-2-lipgloss.Height(legenda), m.navCidades.colInicio, m.navCidades.linhaInicio, cursor)
	if len(t.rotulos) == 1 && m.busca.Value() != "" {
		tabela += "\n" + m.est.fraco.Render(" Nenhuma cidade de "+nomesUF[m.ufCidades]+" com “"+m.busca.Value()+"” no nome.")
	}
	return topo + "\n" + tabela + "\n" + legenda
}
