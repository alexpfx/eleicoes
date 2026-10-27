package main

import (
	"cmp"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Resultado por bairro: lido dos JSON que o comando cmd/bairros gera com os dados abertos do TSE
// (votos por seção somados pelo bairro do local de votação). Os dados não mudam durante a
// apuração; um turno novo entra quando o TSE o publica e "make bairros" roda de novo.

// dadosBairros são os JSON de dados/bairros, embutidos no executável na compilação, para quem
// baixa o programa pronto não precisar gerá-los. A opção -bairros usa os de uma pasta no lugar.
//
//go:embed dados/bairros/*.json
var dadosBairros embed.FS

// arquivosBairros é de onde vêm os JSON por UF: a pasta da opção -bairros ou os embutidos.
func (c config) arquivosBairros() fs.FS {
	if c.pastaBairros != "" {
		return os.DirFS(c.pastaBairros)
	}
	embutidos, err := fs.Sub(dadosBairros, "dados/bairros")
	if err != nil {
		panic(err) // só falha com um caminho inválido, que é constante
	}
	return embutidos
}

// arquivoBairros é o JSON de uma UF gerado por cmd/bairros.
type arquivoBairros struct {
	Turnos      []int                     `json:"turnos"`
	DataTurno   map[int]string            `json:"dataTurno"`
	DataEleicao map[int]string            `json:"dataEleicao"`
	Candidatos  map[int]map[string]string `json:"candidatos"`
	Cidades     map[string][]struct {
		Nome   string `json:"nome"`
		Turnos map[int]struct {
			Secoes         int64            `json:"secoes"`
			Aptos          int64            `json:"aptos"`
			Comparecimento int64            `json:"comparecimento"`
			Abstencoes     int64            `json:"abstencoes"`
			Nominais       int64            `json:"nominais"`
			Brancos        int64            `json:"brancos"`
			Nulos          int64            `json:"nulos"`
			Votos          map[string]int64 `json:"votos"`
		} `json:"turnos"`
	} `json:"cidades"`
}

type bairro struct {
	chave string // cidade + "#" + posição: "sp71072#0012"
	nome  string
	busca string
}

// bairrosUF são os bairros das cidades de uma UF.
type bairrosUF struct {
	turnos      []int
	dataTurno   map[int]string      // geração do arquivo do TSE
	dataEleicao map[int]string      // dia da votação; vazio nos arquivos gerados antes deste campo
	porCidade   map[string][]bairro // chave da cidade -> bairros em ordem alfabética
	porChave    map[string]bairro
}

func (b *bairrosUF) temTurno(turno int) bool { return slices.Contains(b.turnos, turno) }

type msgBairros struct {
	uf         string
	lista      *bairrosUF
	resultados map[alvo]*Resultado
	err        error
}

func ehBairro(chave string) bool { return strings.Contains(chave, "#") }

func (m *modelo) modoBairros() bool { return m.aba == abaCidades && m.cidadeBairros != "" }

// abrirBairros mostra os bairros da cidade e lê o arquivo da UF, se ainda não foi lido.
func (m *modelo) abrirBairros(cidade string) tea.Cmd {
	m.cidadeBairros = cidade
	m.navBairros = navegacao{}
	m.busca.Reset()
	m.busca.Blur()
	uf := cidade[:2]
	if m.bairros[uf] != nil || m.carregandoBairros[uf] {
		return nil
	}
	m.carregandoBairros[uf] = true
	delete(m.erroBairros, uf)
	arquivos, pasta := m.cfg.arquivosBairros(), m.cfg.pastaBairros
	// Nomes de urna e partidos vêm do resultado nacional, que os arquivos por seção não trazem.
	var oficiais [3]map[string]Candidato
	for turno := 1; turno <= 2; turno++ {
		if r := m.dados[alvo{turno, "br"}]; r != nil {
			oficiais[turno] = map[string]Candidato{}
			for _, c := range r.Candidatos {
				oficiais[turno][c.Numero] = Candidato{Nome: c.Nome, Partido: c.Partido}
			}
		}
	}
	return func() tea.Msg {
		lista, resultados, err := lerBairros(arquivos, pasta, uf, oficiais)
		return msgBairros{uf, lista, resultados, err}
	}
}

func (m *modelo) fecharBairros() {
	cidade := m.cidadeBairros
	m.cidadeBairros = ""
	m.busca.Reset()
	m.busca.Blur()
	// Volta com a cidade selecionada na lista.
	m.navCidades.cursor = max(0, slices.Index(m.linhasOrdenadas(), cidade))
}

func (m *modelo) aplicarBairros(msg msgBairros) {
	delete(m.carregandoBairros, msg.uf)
	if msg.err != nil {
		m.erroBairros[msg.uf] = msg.err.Error()
		return
	}
	m.bairros[msg.uf] = msg.lista
	for a, r := range msg.resultados {
		m.dados[a] = r
	}
}

// lerBairros lê o JSON de uma UF e monta um Resultado por bairro e turno, como os do TSE, para
// a tabela e o painel tratarem bairros como qualquer outra linha. A pasta, vazia nos dados
// embutidos, só entra nas mensagens.
func lerBairros(arquivos fs.FS, pasta, uf string, oficiais [3]map[string]Candidato) (*bairrosUF, map[alvo]*Resultado, error) {
	caminho := uf + ".json"
	if pasta != "" {
		caminho = filepath.Join(pasta, caminho)
	}
	b, err := fs.ReadFile(arquivos, uf+".json")
	switch {
	case errors.Is(err, fs.ErrNotExist) && pasta == "":
		return nil, nil, fmt.Errorf("esta versão do programa não traz os bairros de %s", nomesUF[uf])
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil, fmt.Errorf("ainda não há dados de bairros (%s); rode \"make bairros\", que baixa ~500 MB de dados abertos do TSE", caminho)
	case err != nil:
		return nil, nil, err
	}
	var a arquivoBairros
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", caminho, err)
	}
	// Só valem os turnos com votos por candidato, que trazem a data da votação por seção. Um turno
	// só com o detalhe (comparecimento, brancos e nulos), de um arquivo gerado enquanto o TSE não
	// publicava a votação, fica de fora.
	var turnos []int
	for _, turno := range a.Turnos {
		if a.DataTurno[turno] != "" {
			turnos = append(turnos, turno)
		}
	}
	lista := &bairrosUF{turnos: turnos, dataTurno: a.DataTurno, dataEleicao: a.DataEleicao, porCidade: map[string][]bairro{}, porChave: map[string]bairro{}}
	resultados := map[alvo]*Resultado{}
	for mun, bs := range a.Cidades {
		cidade := uf + mun
		for i, bj := range bs {
			if !slices.ContainsFunc(turnos, func(turno int) bool { _, ok := bj.Turnos[turno]; return ok }) {
				continue
			}
			nome := nomeProprio(bj.Nome)
			br := bairro{chave: fmt.Sprintf("%s#%04d", cidade, i), nome: nome, busca: paraBusca(nome)}
			lista.porCidade[cidade] = append(lista.porCidade[cidade], br)
			lista.porChave[br.chave] = br
			for turno, t := range bj.Turnos {
				if !lista.temTurno(turno) {
					continue
				}
				r := &Resultado{
					UF: br.chave, Atualizacao: a.DataTurno[turno], Concluida: true,
					Secoes: t.Secoes, SecoesTotalizadas: t.Secoes, PctSecoes: 100,
					Eleitorado: t.Aptos, EleitoradoApurado: t.Aptos,
					Comparecimento: t.Comparecimento, Abstencao: t.Abstencoes,
					PctComparecimento: pct(t.Comparecimento, t.Comparecimento+t.Abstencoes),
					PctAbstencao:      pct(t.Abstencoes, t.Comparecimento+t.Abstencoes),
					TotalVotos:        t.Nominais + t.Brancos + t.Nulos,
					Validos:           t.Nominais, Brancos: t.Brancos, Nulos: t.Nulos,
				}
				r.PctValidos, r.PctBrancos, r.PctNulos = pct(r.Validos, r.TotalVotos), pct(r.Brancos, r.TotalVotos), pct(r.Nulos, r.TotalVotos)
				for numero, votos := range t.Votos {
					c, ok := oficiais[turno][numero]
					if !ok {
						c = Candidato{Nome: a.Candidatos[turno][numero]}
					}
					c.Numero, c.Votos, c.Pct = numero, votos, pct(votos, t.Nominais)
					r.Candidatos = append(r.Candidatos, c)
				}
				slices.SortFunc(r.Candidatos, func(x, y Candidato) int {
					return cmp.Or(cmp.Compare(y.Votos, x.Votos), cmp.Compare(numero(x.Numero), numero(y.Numero)))
				})
				resultados[alvo{turno, br.chave}] = r
			}
		}
	}
	return lista, resultados, nil
}

// bairrosFiltrados são os bairros da cidade aberta cujo nome contém o texto da busca.
func (m *modelo) bairrosFiltrados() []string {
	lista := m.bairros[m.cidadeBairros[:2]]
	if lista == nil {
		return nil
	}
	busca := paraBusca(m.busca.Value())
	var chaves []string
	for _, b := range lista.porCidade[m.cidadeBairros] {
		if strings.Contains(b.busca, busca) {
			chaves = append(chaves, b.chave)
		}
	}
	return chaves
}

// turnoOrdem é o turno usado na ordem por candidato: o 2º, ou o 1º nos bairros enquanto o TSE
// não publica a votação por seção do 2º turno.
func (m *modelo) turnoOrdem() int {
	if m.modoBairros() {
		if lista := m.bairros[m.cidadeBairros[:2]]; lista != nil && !lista.temTurno(2) {
			return 1
		}
	}
	return 2
}

func (m *modelo) nomeBairro(chave string) string {
	if lista := m.bairros[chave[:2]]; lista != nil {
		if b, ok := lista.porChave[chave]; ok {
			return b.nome
		}
	}
	return chave
}

// legendaBairros vem antes da legenda comum, na tela dos bairros de uma cidade.
func (m *modelo) legendaBairros() []string {
	linhas := []string{"Bairro do local de votação (nem sempre é onde o eleitor mora) · votos por seção dos dados abertos do TSE, somados por bairro"}
	lista := m.bairros[m.cidadeBairros[:2]]
	if lista == nil {
		return linhas
	}
	for _, turno := range lista.turnos {
		data := lista.dataEleicao[turno]
		if data == "" {
			data, _, _ = strings.Cut(lista.dataTurno[turno], " ")
		}
		linhas[0] += fmt.Sprintf(" · %dº turno de %s", turno, data)
	}
	switch {
	case lista.temTurno(2):
	case m.cfg.pastaBairros == "":
		linhas = append(linhas, "2º turno por bairro: o TSE publica a votação por seção dias depois da eleição; ele entra numa versão nova do programa")
	default:
		linhas = append(linhas, "2º turno por bairro: o TSE ainda não publicou a votação por seção; quando publicar, rode \"make bairros\" e aperte r aqui")
	}
	return linhas
}

// resumoBairros é o começo da linha acima da tabela dos bairros.
func (m *modelo) resumoBairros() string {
	e := m.est
	esq := " " + e.fraco.Render(nomesUF[m.ufCidades]+" › ") + e.negrito.Render(m.nomeCidade(m.cidadeBairros))
	if lista := m.bairros[m.cidadeBairros[:2]]; lista != nil {
		total := len(lista.porCidade[m.cidadeBairros])
		qtd := fmtInt(int64(total)) + " bairros"
		if m.busca.Value() != "" {
			qtd = fmt.Sprintf("%s de %s bairros", fmtInt(int64(len(m.bairrosFiltrados()))), fmtInt(int64(total)))
		}
		esq += e.fraco.Render(" · " + qtd + " · ordem: " + m.descricaoOrdem())
	}
	return esq
}

func (m *modelo) nomeCidade(chave string) string {
	if m.municipios != nil {
		if mu, ok := m.municipios.porChave[chave]; ok {
			return mu.nome
		}
	}
	return chave
}

// corpoBairros é a tabela dos bairros, ou o aviso enquanto o arquivo não foi lido.
func (m *modelo) corpoBairros(topo string, altura int) string {
	uf := m.cidadeBairros[:2]
	switch {
	case m.erroBairros[uf] != "":
		return topo + "\n\n" + m.est.aviso.Width(m.largura).Render(" "+m.erroBairros[uf]) +
			"\n" + m.est.fraco.Render(" esc volta para as cidades")
	case m.bairros[uf] == nil:
		return topo + "\n\n" + m.est.fraco.Render(" Lendo os bairros…")
	}
	t, ok := m.montarTabela()
	if !ok {
		return topo + "\n\n" + m.est.fraco.Render(" Aguardando os dados dos candidatos do 2º turno…")
	}
	cursor := m.navBairros.cursor
	if m.telaLimpa() {
		cursor = -1
	}
	legenda := m.legendaRenderizada()
	tabela := t.render(m.est, m.largura, altura-2-lipgloss.Height(legenda), m.navBairros.colInicio, m.navBairros.linhaInicio, cursor)
	if len(t.rotulos) == 1 && m.busca.Value() != "" {
		tabela += "\n" + m.est.fraco.Render(" Nenhum bairro de "+m.nomeCidade(m.cidadeBairros)+" com “"+m.busca.Value()+"” no nome.")
	}
	return topo + "\n" + tabela + "\n" + legenda
}

// rotuloBairro encurta o nome do bairro (ou, na linha do total, o da cidade) para a tabela.
func (m *modelo) rotuloBairro(chave string) string {
	if ehBairro(chave) {
		return ansi.Truncate(m.nomeBairro(chave), larguraNomeCidade, "…")
	}
	return ansi.Truncate("Total "+m.nomeCidade(chave), larguraNomeCidade, "…")
}
