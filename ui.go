package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"image/color"
	"maps"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type aba int

const (
	abaPainel aba = iota
	abaTabela
	abaCidades
	numAbas
)

type (
	msgTique  time.Time
	msgRodada rodada
)

type teclas struct {
	Aba, AbaAnterior, Painel, Tabela, Cidades, Cima, Baixo, PagCima, PagBaixo, Inicio, Fim  key.Binding
	Esquerda, Direita, ColEsquerda, ColDireita, Abrir, AbrirCidades, Bairros, Buscar        key.Binding
	LimparBusca                                                                             key.Binding
	Variacao, VotosTurno1, VotosMil, Ordem, Turno, Apresentacao, Atualizar, Sair, Foto, CSV key.Binding
	// Só para a ajuda: resumem pares de teclas numa entrada ou descrevem as teclas da busca.
	ajudaLinhas, ajudaColunas, ajudaEstados, ajudaConfirmar, ajudaLimpar, ajudaMover, ajudaVoltar key.Binding
}

func novasTeclas() teclas {
	b := func(teclas []string, atalho, desc string) key.Binding {
		return key.NewBinding(key.WithKeys(teclas...), key.WithHelp(atalho, desc))
	}
	return teclas{
		Aba:          b([]string{"tab"}, "tab", "tela"),
		AbaAnterior:  b([]string{"shift+tab"}, "shift+tab", "tela anterior"),
		Painel:       b([]string{"1"}, "1", "painel"),
		Tabela:       b([]string{"2"}, "2", "por estado"),
		Cidades:      b([]string{"3"}, "3", "cidades"),
		Cima:         b([]string{"up", "k"}, "↑", "subir"),
		Baixo:        b([]string{"down", "j"}, "↓", "descer"),
		PagCima:      b([]string{"pgup"}, "pgup", "página acima"),
		PagBaixo:     b([]string{"pgdown", "space"}, "pgdown", "página abaixo"),
		Inicio:       b([]string{"home", "g"}, "home", "início"),
		Fim:          b([]string{"end", "G"}, "end", "fim"),
		Esquerda:     b([]string{"left", "h"}, "←", "colunas à esquerda"),
		Direita:      b([]string{"right", "l"}, "→", "colunas à direita"),
		ColEsquerda:  b([]string{"shift+left", "H"}, "shift+←", "colunas à esquerda"),
		ColDireita:   b([]string{"shift+right", "L"}, "shift+→", "colunas à direita"),
		Abrir:        b([]string{"enter"}, "enter", "painel"),
		AbrirCidades: b([]string{"c"}, "c", "cidades"),
		Bairros:      b([]string{"b"}, "b", "bairros"),
		Buscar:       b([]string{"/"}, "/", "buscar"),
		LimparBusca:  b([]string{"esc"}, "esc", "limpar busca"),
		Variacao:     b([]string{"v"}, "v", "pontos/%"),
		VotosTurno1:  b([]string{"o"}, "o", "votos 1º"),
		VotosMil:     b([]string{"m"}, "m", "milhares"),
		Ordem:        b([]string{"s"}, "s", "ordem"),
		Turno:        b([]string{"t"}, "t", "turno"),
		Apresentacao: b([]string{"p"}, "p", "apresentação"),
		Foto:         b([]string{"f"}, "f", "foto"),
		CSV:          b([]string{"e"}, "e", "csv"),
		Atualizar:    b([]string{"r"}, "r", "atualizar"),
		// Sem esc: é a tecla de sair da busca, e um esc a mais não pode fechar a transmissão.
		Sair: b([]string{"q", "ctrl+c"}, "q", "sair"),
		// Uma binding sem teclas conta como desativada e some da ajuda.
		ajudaLinhas:    b([]string{"up", "down"}, "↑↓", "rolar"),
		ajudaColunas:   b([]string{"left", "right"}, "←→", "colunas"),
		ajudaEstados:   b([]string{"left", "right"}, "←→", "estado"),
		ajudaConfirmar: b([]string{"enter"}, "enter", "fechar a busca"),
		ajudaLimpar:    b([]string{"esc"}, "esc", "limpar"),
		ajudaMover:     b([]string{"up", "down"}, "↑↓", "escolher"),
		ajudaVoltar:    b([]string{"esc"}, "esc", "cidades"),
	}
}

// ajuda implementa help.KeyMap com as teclas que valem na aba atual.
type ajuda []key.Binding

func (a ajuda) ShortHelp() []key.Binding  { return a }
func (a ajuda) FullHelp() [][]key.Binding { return [][]key.Binding{a} }

type modelo struct {
	cfg        config
	cli        *Cliente
	eleicoes   *Eleicoes
	dados      map[alvo]*Resultado
	etags      map[string]string
	anteriores map[string]int64 // votos do painel na versão anterior, para mostrar o "+N"

	aba              aba
	ufPainel         string // "br", UF ou cidade ("sp71072") mostrada no painel
	turnoPainel      int
	painelInicio     int
	navEstados       navegacao
	navCidades       navegacao
	variacaoRelativa bool // tecla v: variação relativa do percentual em vez da diferença em pontos
	votosTurno1      bool // o 1º turno mostra só os percentuais até a tecla "o" pedir os votos
	votosMil         bool // tecla m: votos em milhares em vez da quantidade inteira

	// Tela Cidades: a lista de municípios vem do TSE na primeira visita; as cidades de cada
	// estado são baixadas quando ele aparece na tela.
	ufCidades            string
	municipios           *listaMunicipios
	carregandoMunicipios bool
	erroMunicipios       string
	cargas               map[string]*carga // por UF
	avisoCidades         string
	pausaCidades         time.Time // depois de um HTTP 429, as cidades esperam até aqui
	busca                textinput.Model

	// Bairros de uma cidade (tecla b na tela Cidades), lidos dos JSON de cmd/bairros.
	cidadeBairros     string // cidade aberta; "" mostra a lista de cidades
	navBairros        navegacao
	bairros           map[string]*bairrosUF // por UF
	carregandoBairros map[string]bool
	erroBairros       map[string]string

	largura, altura int
	est             estilos
	spinner         spinner.Model
	help            help.Model
	teclas          teclas

	buscando       bool
	contagem       int
	ultimaConsulta time.Time
	aviso          string
	concluido      bool

	// Modo apresentação: depois de cfg.apresentacao segundos sem tecla, alterna as abas sozinho.
	apresentacao bool
	ultimaTecla  time.Time
	proximaTroca time.Time

	// Tecla f: a foto usa a tela limpa e as cores do terminal (nil se ele não informou).
	fotografando       bool
	corFundo, corTexto color.Color
	mensagem           string // confirmação passageira no rodapé, até mensagemAte
	mensagemAte        time.Time
}

// telaLimpa tira da tela o que só serve para quem opera o programa (atalhos, linha selecionada,
// dicas de busca): no modo apresentação, para a transmissão, e nas fotos.
func (m *modelo) telaLimpa() bool { return m.apresentacao || m.fotografando }

// navegacao guarda a posição e a ordem de uma tabela; cada tela tem a sua.
type navegacao struct {
	cursor      int // linha selecionada (0 = totalizador)
	linhaInicio int // primeira linha visível abaixo do totalizador
	colInicio   int // primeira coluna rolável visível
	ordem       ordem
}

func (m *modelo) nav() *navegacao {
	switch {
	case m.modoBairros():
		return &m.navBairros
	case m.aba == abaCidades:
		return &m.navCidades
	}
	return &m.navEstados
}

// ordens são as ordens que a tecla s percorre em cada tela. Nas cidades não há votos a vir, e nos
// bairros, já totalizados, também não há o % apurado.
func (m *modelo) ordens() []ordem {
	switch {
	case m.modoBairros():
		return []ordem{porUF, porCandidato2, porCandidato1}
	case m.aba == abaCidades:
		return []ordem{porUF, porApuracao, porCandidato2, porCandidato1}
	}
	return []ordem{porUF, porApuracao, porCandidato2, porCandidato1, porAVir}
}

func novoModelo(cfg config, cli *Cliente) *modelo {
	m := &modelo{
		cfg: cfg, cli: cli,
		dados: map[alvo]*Resultado{}, etags: map[string]string{},
		ufPainel: cfg.uf, turnoPainel: cfg.turno,
		ufCidades: ufs[0], cargas: map[string]*carga{}, busca: novaBusca(true),
		bairros: map[string]*bairrosUF{}, carregandoBairros: map[string]bool{}, erroBairros: map[string]string{},
		est:     novosEstilos(true),
		spinner: spinner.New(spinner.WithSpinner(spinner.Dot)),
		help:    help.New(),
		teclas:  novasTeclas(),
		// Sem nenhuma tecla desde a abertura, a apresentação começa após o tempo de inatividade.
		ultimaTecla: time.Now(),
	}
	if cfg.uf != "br" {
		m.ufCidades = cfg.uf
	}
	return m
}

func (m *modelo) entrarApresentacao(agora time.Time) {
	m.apresentacao = true
	m.proximaTroca = agora.Add(time.Duration(m.cfg.troca) * time.Second)
	m.painelInicio = 0
	for _, n := range []*navegacao{&m.navEstados, &m.navCidades} {
		n.linhaInicio, n.colInicio = 0, 0
	}
}

// avancarApresentacao roda a cada tique: entra na apresentação após a inatividade e, dentro
// dela, alterna entre o painel e a tabela por estado no tempo de cada tela.
func (m *modelo) avancarApresentacao(agora time.Time) {
	switch {
	case m.apresentacao:
		if !agora.Before(m.proximaTroca) {
			m.aba = map[bool]aba{true: abaTabela, false: abaPainel}[m.aba == abaPainel]
			m.proximaTroca = agora.Add(time.Duration(m.cfg.troca) * time.Second)
		}
	case m.cfg.apresentacao > 0 && agora.Sub(m.ultimaTecla) >= time.Duration(m.cfg.apresentacao)*time.Second:
		m.entrarApresentacao(agora)
	}
}

func (m *modelo) simulacao() bool { return m.cfg.base != baseTSE }

func (m *modelo) fonte() string {
	if m.simulacao() {
		return "servidor de simulação"
	}
	return "TSE"
}

func (m *modelo) alvoPainel() alvo { return alvo{m.turnoPainel, m.ufPainel} }

// turnoConcluido: os arquivos de BR, estados e ZZ do turno chegaram e estão totalizados.
func (m *modelo) turnoConcluido(turno int) bool {
	for _, uf := range linhasTabela {
		if r := m.dados[alvo{turno, uf}]; r == nil || !r.Concluida {
			return false
		}
	}
	return true
}

// candidatosSegundoTurno devolve os dois candidatos do 2º turno em ordem fixa, pelo número
// (13 antes de 22). Não usa o "seq" do TSE: ele é a classificação e muda a cada virada,
// o que trocaria as colunas de lugar.
func (m *modelo) candidatosSegundoTurno() ([2]Candidato, bool) {
	var cands []Candidato
	if r := m.dados[alvo{2, "br"}]; r != nil && len(r.Candidatos) >= 2 {
		cands = slices.Clone(r.Candidatos)
	} else if r := m.dados[alvo{1, "br"}]; r != nil {
		for _, c := range r.Candidatos {
			if strings.EqualFold(c.Situacao, "2º turno") {
				cands = append(cands, c)
			}
		}
	}
	slices.SortFunc(cands, func(a, b Candidato) int { return cmp.Compare(numero(a.Numero), numero(b.Numero)) })
	if len(cands) < 2 {
		return [2]Candidato{}, false
	}
	return [2]Candidato{cands[0], cands[1]}, true
}

func (m *modelo) corCandidato(numero string) color.Color {
	if cands, ok := m.candidatosSegundoTurno(); ok {
		for i, c := range cands {
			if c.Numero == numero {
				return m.est.corCand[i]
			}
		}
	}
	return m.est.corOutros
}

func votosPorNumero(r *Resultado) map[string]int64 {
	v := make(map[string]int64, len(r.Candidatos))
	for _, c := range r.Candidatos {
		v[c.Numero] = c.Votos
	}
	return v
}

func resumoErro(err error) string {
	var erroRede net.Error
	switch {
	case errors.As(err, &erroRede), errors.Is(err, context.DeadlineExceeded):
		return "sem resposta"
	case naoEncontrado(err):
		return "não publicado"
	case limitado(err):
		return "excesso de consultas, HTTP 429"
	}
	return err.Error()
}

// aplicar incorpora uma rodada de consultas ao estado. Só é chamada a partir do Update.
func (m *modelo) aplicar(r rodada) {
	m.ultimaConsulta = time.Now()
	if r.err != nil {
		m.aviso = fmt.Sprintf("não foi possível ler a configuração em %s (%s)", m.cfg.base, resumoErro(r.err))
		return
	}
	if r.eleicoes != nil {
		m.eleicoes = r.eleicoes
	}

	naoPublicados, falhas, primeiraFalha := m.guardar(r.itens)
	if slices.ContainsFunc(r.itens, func(it item) bool { return limitado(it.err) }) {
		m.pausarCidades()
	}
	// As cargas de cidades que falharam são refeitas a cada rodada, até dar certo.
	for _, c := range m.cargas {
		c.refazer = c.refazer || (c.falhou && !c.ativa)
	}

	var avisos []string
	if falhas > 0 {
		avisos = append(avisos, fmt.Sprintf("%d de %d consultas ao %s falharam (%s); nova tentativa na próxima rodada",
			falhas, len(r.itens), m.fonte(), resumoErro(primeiraFalha)))
	}
	if naoPublicados > 0 {
		avisos = append(avisos, fmt.Sprintf("%d arquivos ainda não publicados", naoPublicados))
	}
	m.aviso = strings.Join(avisos, " · ")
	m.concluido = m.eleicoes != nil && m.eleicoes.Codigo[2] != 0 && m.turnoConcluido(2)
}

// guardar incorpora os arquivos que mudaram e conta os que faltam ou falharam. Quando muda o
// arquivo do painel, guarda os votos anteriores para o "+N".
func (m *modelo) guardar(itens []item) (naoPublicados, falhas int, primeiraFalha error) {
	painel := m.alvoPainel()
	antes := m.dados[painel]
	for _, it := range itens {
		switch {
		case it.err == nil:
			if it.dados != nil {
				m.dados[it.alvo] = it.dados
				m.etags[it.url] = it.etag
			}
		case naoEncontrado(it.err):
			naoPublicados++
		default:
			falhas++
			primeiraFalha = cmp.Or(primeiraFalha, it.err)
		}
	}
	if depois := m.dados[painel]; antes != nil && depois != nil && depois.Idg != antes.Idg {
		m.anteriores = votosPorNumero(antes)
	}
	return naoPublicados, falhas, primeiraFalha
}

func tique() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return msgTique(t) })
}

func (m *modelo) iniciarBusca() tea.Cmd {
	m.buscando = true
	cli, ciclo, ele := m.cli, m.cfg.ciclo, m.eleicoes
	etags, pular1 := maps.Clone(m.etags), m.turnoConcluido(1)
	buscar := func() tea.Msg {
		ctx, cancelar := context.WithTimeout(context.Background(), time.Minute)
		defer cancelar()
		return msgRodada(cli.buscarTudo(ctx, ciclo, ele, etags, pular1))
	}
	return tea.Batch(buscar, m.spinner.Tick)
}

func (m *modelo) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, tea.RequestForegroundColor, tique(), m.iniciarBusca())
}

func (m *modelo) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.largura, m.altura = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
	case tea.ForegroundColorMsg:
		m.corTexto = msg.Color
	case msgSalvo:
		m.aplicarSalvo(msg)
	case tea.BackgroundColorMsg:
		m.corFundo = msg.Color
		m.est = novosEstilos(msg.IsDark())
		m.help.Styles = help.DefaultStyles(msg.IsDark())
		busca := novaBusca(msg.IsDark())
		busca.SetValue(m.busca.Value())
		m.busca = busca
	case tea.KeyPressMsg:
		// Foto e CSV não interrompem a apresentação: dá para salvar a tela no meio da live.
		switch {
		case m.busca.Focused() || m.largura == 0:
		case key.Matches(msg, m.teclas.Foto):
			return m, m.tirarFoto()
		case key.Matches(msg, m.teclas.CSV):
			return m, m.exportarCSV()
		}
		// Qualquer outra tecla devolve o controle ao usuário e ainda faz a sua ação normal.
		m.ultimaTecla, m.apresentacao = time.Now(), false
		cmd := m.tecla(msg)
		m.ajustarRolagem()
		return m, tea.Batch(cmd, m.carregarCidades())
	case msgTique:
		m.avancarApresentacao(time.Time(msg))
		// Fim da pausa pedida pelo TSE: as cidades voltam a ser baixadas sem esperar a rodada.
		if !m.pausaCidades.IsZero() && !time.Time(msg).Before(m.pausaCidades) {
			m.pausaCidades, m.avisoCidades = time.Time{}, ""
			if cmd := m.carregarCidades(); cmd != nil {
				return m, tea.Batch(tique(), cmd)
			}
		}
		// Um só laço de 1 s: conta até a próxima consulta e só dispara se nenhuma estiver em curso.
		if !m.buscando && !m.concluido {
			if m.contagem--; m.contagem <= 0 {
				return m, tea.Batch(tique(), m.iniciarBusca())
			}
		}
		return m, tique()
	case msgRodada:
		m.buscando = false
		m.aplicar(rodada(msg))
		m.contagem = m.cfg.intervalo
		m.ajustarRolagem()
		return m, m.carregarCidades()
	case msgMunicipios:
		m.aplicarMunicipios(msg)
		m.ajustarRolagem()
		return m, m.carregarCidades()
	case msgLoteCidades:
		cmd := m.aplicarLote(msg)
		m.ajustarRolagem()
		return m, cmd
	case msgBairros:
		m.aplicarBairros(msg)
	case spinner.TickMsg:
		if m.buscando {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	m.ajustarRolagem()
	return m, nil
}

func (m *modelo) tecla(msg tea.KeyPressMsg) tea.Cmd {
	if m.busca.Focused() {
		return m.teclaBusca(msg)
	}
	k, nav := m.teclas, m.nav()
	pagina := m.linhasVisiveis()
	tabela := m.aba != abaPainel // as teclas da tabela valem nas telas Por estado e Cidades
	cidades := m.aba == abaCidades
	switch {
	case key.Matches(msg, k.Sair):
		return tea.Quit
	case key.Matches(msg, k.Aba):
		m.aba = (m.aba + 1) % numAbas
	case key.Matches(msg, k.AbaAnterior):
		m.aba = (m.aba + numAbas - 1) % numAbas
	case key.Matches(msg, k.Painel):
		m.aba = abaPainel
	case key.Matches(msg, k.Tabela):
		m.aba = abaTabela
	case key.Matches(msg, k.Cidades):
		m.aba = abaCidades
	case key.Matches(msg, k.Apresentacao):
		m.entrarApresentacao(time.Now())
	case key.Matches(msg, k.Atualizar):
		var cmds []tea.Cmd
		if cidades {
			m.recarregarCidades()
		}
		if m.modoBairros() {
			// Relê o arquivo da UF: depois de um "make bairros", o 2º turno entra sem reabrir.
			delete(m.bairros, m.cidadeBairros[:2])
			cmds = append(cmds, m.abrirBairros(m.cidadeBairros))
		}
		if !m.buscando {
			cmds = append(cmds, m.iniciarBusca())
		}
		return tea.Batch(cmds...)
	case key.Matches(msg, k.Cima):
		m.mover(-1)
	case key.Matches(msg, k.Baixo):
		m.mover(1)
	case key.Matches(msg, k.PagCima):
		m.mover(-pagina)
	case key.Matches(msg, k.PagBaixo):
		m.mover(pagina)
	case key.Matches(msg, k.Inicio):
		m.mover(-len(m.linhasBase()) - 1000)
	case key.Matches(msg, k.Fim):
		m.mover(len(m.linhasBase()) + 1000)
	case cidades && !m.modoBairros() && key.Matches(msg, k.Esquerda):
		m.trocarEstado(-1)
	case cidades && !m.modoBairros() && key.Matches(msg, k.Direita):
		m.trocarEstado(1)
	case cidades && key.Matches(msg, k.Buscar):
		return m.busca.Focus()
	case cidades && key.Matches(msg, k.LimparBusca) && m.busca.Value() != "":
		m.limparBusca()
	case cidades && key.Matches(msg, k.LimparBusca, k.Bairros) && m.modoBairros():
		m.fecharBairros()
	case cidades && key.Matches(msg, k.Bairros):
		if chave := m.linhasOrdenadas()[nav.cursor]; ehCidade(chave) {
			return m.abrirBairros(chave)
		}
	case tabela && key.Matches(msg, k.Esquerda, k.ColEsquerda):
		nav.colInicio--
	case tabela && key.Matches(msg, k.Direita, k.ColDireita):
		nav.colInicio++
	case tabela && key.Matches(msg, k.Variacao):
		m.variacaoRelativa = !m.variacaoRelativa
	case tabela && key.Matches(msg, k.Ordem):
		// A linha selecionada continua na mesma UF, cidade ou bairro depois de reordenar.
		ordens := m.ordens()
		chave := m.linhasOrdenadas()[nav.cursor]
		nav.ordem = ordens[(slices.Index(ordens, nav.ordem)+1)%len(ordens)]
		nav.cursor = max(0, slices.Index(m.linhasOrdenadas(), chave))
	case tabela && key.Matches(msg, k.VotosMil):
		m.votosMil = !m.votosMil
	case tabela && key.Matches(msg, k.VotosTurno1):
		m.votosTurno1 = !m.votosTurno1
	case tabela && key.Matches(msg, k.Abrir):
		// Um bairro abre no turno que já tem dados por seção.
		chave := m.linhasOrdenadas()[nav.cursor]
		turno := 2
		if ehBairro(chave) {
			turno = m.turnoOrdem()
		}
		m.mostrarNoPainel(chave, turno)
	case m.aba == abaTabela && key.Matches(msg, k.AbrirCidades):
		if uf := m.linhasOrdenadas()[nav.cursor]; uf != "br" {
			m.mudarEstadoCidades(uf)
		}
		m.aba = abaCidades
	case m.aba == abaPainel && key.Matches(msg, k.Turno):
		m.mostrarNoPainel(m.ufPainel, 3-m.turnoPainel)
	}
	return nil
}

func (m *modelo) mostrarNoPainel(uf string, turno int) {
	m.ufPainel, m.turnoPainel = uf, turno
	m.aba, m.painelInicio, m.anteriores = abaPainel, 0, nil
}

func (m *modelo) mover(d int) {
	if m.aba == abaPainel {
		m.painelInicio += d
	} else {
		m.nav().cursor += d
	}
}

// ajustarRolagem mantém cursor e deslocamentos dentro dos limites e a linha selecionada à vista.
func (m *modelo) ajustarRolagem() {
	nav := m.nav()
	total := len(m.linhasBase())
	nav.cursor = min(max(nav.cursor, 0), total-1)
	n := m.linhasVisiveis()
	if nav.cursor >= 1 {
		if i := nav.cursor - 1; i < nav.linhaInicio {
			nav.linhaInicio = i
		} else if i >= nav.linhaInicio+n {
			nav.linhaInicio = i - n + 1
		}
	}
	nav.linhaInicio = min(max(nav.linhaInicio, 0), max(0, total-1-n))
	if t, ok := m.montarTabela(); ok && m.largura > 0 {
		nav.colInicio = min(max(nav.colInicio, 0), t.maxInicio(m.largura))
	} else {
		nav.colInicio = max(nav.colInicio, 0)
	}
	painel := lipgloss.Height(m.renderPainel(max(m.largura, 1)))
	m.painelInicio = min(max(m.painelInicio, 0), max(0, painel-m.alturaCorpo()))
}

func (m *modelo) renderTopo() string {
	e := m.est
	var linhas []string
	if m.simulacao() {
		faixa := ansi.Truncate(m.faixaSimulacao(), m.largura, "…")
		linhas = append(linhas, e.simulacao.Render(alinharEsquerda(faixa, m.largura)))
	}
	marca := " ELEIÇÕES 2026 · PRESIDENTE "
	if m.largura < 100 {
		marca = " ELEIÇÕES 2026 "
	}
	esq := e.marca.Render(marca)
	for i, nome := range []string{"1 Painel", "2 Por estado", "3 Cidades"} {
		estilo := e.abaInativa
		if aba(i) == m.aba {
			estilo = e.abaAtiva
		}
		esq += "  " + estilo.Render(nome)
	}
	var status string
	switch {
	case m.buscando:
		status = m.spinner.View() + "consultando…"
	case m.concluido:
		status = "✓ totalização concluída"
	default:
		status = fmt.Sprintf("próxima em %ds", max(m.contagem, 0))
	}
	if m.apresentacao {
		status = "▶ apresentação · " + status
	}
	switch {
	case m.fotografando:
		// Na foto, a contagem para a próxima consulta não diz nada; vale o horário dos dados.
		status = ""
		if !m.ultimaConsulta.IsZero() {
			status = "dados consultados às " + m.ultimaConsulta.Format("15:04:05")
		}
	case !m.ultimaConsulta.IsZero():
		// O horário da última consulta só entra se couber.
		longo := status + " · última " + m.ultimaConsulta.Format("15:04:05")
		if lipgloss.Width(esq)+lipgloss.Width(longo)+2 <= m.largura {
			status = longo
		}
	}
	linhas = append(linhas, juntarPontas(esq, e.status.Render(status)+" ", m.largura), "")
	return strings.Join(linhas, "\n")
}

func (m *modelo) legendaTabela(interativo bool) []string {
	variacao := "Variação: diferença entre o % do 2º turno e o % do 1º turno, em pontos percentuais (ex.: de 45,2% para 49,2% = +4,0)"
	if m.variacaoRelativa {
		variacao = "Variação: quanto o % do 2º turno cresceu ou caiu em relação ao % do 1º turno (ex.: de 45,2% para 49,2% = +8,8%)"
	}
	if interativo {
		variacao += " · v alterna"
	}
	porcentagens := "%: candidatos sobre os votos válidos, B+N sobre o total de votos, abstenção sobre o eleitorado"
	milhares := ""
	if m.votosMil {
		milhares = " · votos em milhares"
	}
	switch {
	case m.modoBairros():
		return append(m.legendaBairros(),
			"* B+N: brancos + nulos · ABST.: abstenção · Total: a cidade inteira, pelo resultado oficial do TSE"+milhares,
			porcentagens,
			variacao,
		)
	case m.aba == abaCidades:
		return []string{
			"* B+N: brancos + nulos · ABST.: abstenção · APUR.: urnas apuradas · Total: o estado inteiro" + milhares,
			porcentagens,
			variacao,
		}
	}
	return []string{
		"* B+N: brancos + nulos · ABST.: abstenção · HORA: última atualização no TSE · APUR.: urnas apuradas · EXT: exterior" + milhares,
		"A VIR: estimativa de votos válidos ainda por apurar, em milhões (eleitores das seções não apuradas × comparecimento × válidos)",
		porcentagens,
		variacao,
	}
}

// linhasVisiveis é quantas linhas cabem abaixo do resumo (e, nas cidades, das abas dos estados),
// do cabeçalho e do totalizador.
func (m *modelo) linhasVisiveis() int {
	acima := 1
	if m.aba == abaCidades {
		acima = 2
	}
	return max(1, m.alturaCorpo()-acima-linhasFixasTabela-lipgloss.Height(m.legendaRenderizada()))
}

// legendaRenderizada é a legenda que vai logo abaixo da tabela, quebrada na largura da tela.
func (m *modelo) legendaRenderizada() string {
	var linhas []string
	for _, l := range m.legendaTabela(!m.telaLimpa()) {
		linhas = append(linhas, m.est.fraco.Width(m.largura).Render(l))
	}
	return strings.Join(linhas, "\n")
}

// resumoApuracao mostra, acima da tabela, o andamento do 2º turno no país.
func (m *modelo) resumoApuracao() string {
	r := m.dados[alvo{2, "br"}]
	switch {
	case r == nil:
		return m.est.fraco.Render(" 2º turno: aguardando os dados")
	case r.SecoesTotalizadas == 0:
		return m.est.fraco.Render(" 2º turno: a apuração ainda não começou")
	}
	return " " + barraSecoes(r.PctSecoes, 16) + "  " +
		m.est.negrito.Render("2º turno: "+fmtPct(r.PctSecoes)+" das seções totalizadas") +
		m.est.fraco.Render(" · dados de "+r.Atualizacao+" · ordem: "+m.descricaoOrdem())
}

func (m *modelo) faixaSimulacao() string {
	fonte := m.cfg.base
	if u, err := url.Parse(m.cfg.base); err == nil && u.Host != "" {
		fonte = u.Host
	}
	return " SIMULAÇÃO — dados fictícios, não oficiais · fonte: " + fonte + " "
}

func (m *modelo) renderRodape() string {
	k := m.teclas
	var linhas []string
	// Nas tabelas, tab e ↑↓ ficam de fora para o resto caber em 160 colunas: as abas do topo e as
	// setas dispensam explicação.
	var atalhos ajuda
	switch {
	case m.busca.Focused():
		atalhos = ajuda{k.ajudaConfirmar, k.ajudaLimpar, k.ajudaMover}
	case m.modoBairros():
		atalhos = ajuda{k.Buscar, k.ajudaVoltar, k.Abrir, k.Variacao, k.VotosTurno1, k.VotosMil, k.Ordem, k.Foto, k.CSV, k.Apresentacao, k.Sair, k.Atualizar}
	case m.aba == abaCidades:
		atalhos = ajuda{k.ajudaEstados, k.Buscar, k.Bairros, k.Abrir, k.Variacao, k.VotosTurno1, k.VotosMil, k.Ordem, k.Foto, k.CSV, k.Apresentacao, k.Sair, k.Atualizar}
	case m.aba == abaTabela:
		atalhos = ajuda{k.ajudaColunas, k.Abrir, k.AbrirCidades, k.Variacao, k.VotosTurno1, k.VotosMil, k.Ordem, k.Foto, k.CSV, k.Apresentacao, k.Sair, k.Atualizar}
	default:
		atalhos = ajuda{k.Aba, k.ajudaLinhas, k.Turno, k.Foto, k.CSV, k.Apresentacao, k.Sair, k.Atualizar}
	}
	avisos := []string{m.aviso}
	if m.aba == abaCidades {
		avisos = append(avisos, m.avisoCidades)
	}
	if aviso := strings.Join(slices.DeleteFunc(avisos, func(a string) bool { return a == "" }), " · "); aviso != "" {
		// O aviso quebra em várias linhas em vez de ser cortado.
		linhas = append(linhas, m.est.aviso.Width(m.largura).Render("⚠ "+aviso))
	}
	if m.mensagem != "" && time.Now().Before(m.mensagemAte) && !m.fotografando {
		linhas = append(linhas, m.est.positivo.Width(m.largura).Render("✓ "+m.mensagem))
	}
	// Na apresentação e nas fotos a tela fica limpa: sem a linha de atalhos.
	if !m.telaLimpa() {
		linhas = append(linhas, m.help.View(atalhos))
	}
	return strings.Join(linhas, "\n")
}

func (m *modelo) alturaCorpo() int {
	return max(1, m.altura-lipgloss.Height(m.renderTopo())-lipgloss.Height(m.renderRodape()))
}

func (m *modelo) corpo(altura int) string {
	switch m.aba {
	case abaCidades:
		return m.corpoCidades(altura)
	case abaTabela:
		t, ok := m.montarTabela()
		if !ok {
			return m.est.fraco.Render(" Aguardando os dados dos candidatos do 2º turno…")
		}
		cursor := m.navEstados.cursor
		if m.telaLimpa() {
			cursor = -1 // sem linha destacada na transmissão e nas fotos
		}
		// A legenda vem logo abaixo da última linha da tabela, e não no rodapé da tela.
		legenda := m.legendaRenderizada()
		tabela := t.render(m.est, m.largura, altura-1-lipgloss.Height(legenda), m.navEstados.colInicio, m.navEstados.linhaInicio, cursor)
		return m.resumoApuracao() + "\n" + tabela + "\n" + legenda
	}
	linhas := strings.Split(m.renderPainel(m.largura), "\n")
	inicio := min(m.painelInicio, len(linhas))
	return strings.Join(linhas[inicio:min(len(linhas), inicio+altura)], "\n")
}

// renderTela monta a tela inteira; a foto usa o mesmo texto.
func (m *modelo) renderTela() string {
	if m.largura == 0 {
		return "Carregando…"
	}
	topo, rodape := m.renderTopo(), m.renderRodape()
	altura := m.alturaCorpo()
	corpo := m.corpo(altura)
	if falta := altura - lipgloss.Height(corpo); falta > 0 {
		corpo += strings.Repeat("\n", falta)
	}
	return cortarLinhas(topo+"\n"+corpo+"\n"+rodape, m.largura)
}

func (m *modelo) View() tea.View {
	v := tea.NewView(m.renderTela())
	v.AltScreen = true
	v.WindowTitle = "Eleições 2026 — Presidente"
	return v
}
