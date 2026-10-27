package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	baseTSE         = "https://resultados.tse.jus.br/oficial"
	cargoPresidente = 1
	consultasJuntas = 6
)

// ufs na ordem das linhas da tabela: estados por sigla e, por último, o exterior.
var ufs = []string{"ac", "al", "am", "ap", "ba", "ce", "df", "es", "go", "ma", "mg", "ms", "mt",
	"pa", "pb", "pe", "pi", "pr", "rj", "rn", "ro", "rr", "rs", "sc", "se", "sp", "to", "zz"}

var nomesUF = map[string]string{
	"br": "Brasil", "ac": "Acre", "al": "Alagoas", "am": "Amazonas", "ap": "Amapá", "ba": "Bahia",
	"ce": "Ceará", "df": "Distrito Federal", "es": "Espírito Santo", "go": "Goiás", "ma": "Maranhão",
	"mg": "Minas Gerais", "ms": "Mato Grosso do Sul", "mt": "Mato Grosso", "pa": "Pará",
	"pb": "Paraíba", "pe": "Pernambuco", "pi": "Piauí", "pr": "Paraná", "rj": "Rio de Janeiro",
	"rn": "Rio Grande do Norte", "ro": "Rondônia", "rr": "Roraima", "rs": "Rio Grande do Sul",
	"sc": "Santa Catarina", "se": "Sergipe", "sp": "São Paulo", "to": "Tocantins", "zz": "Exterior",
}

// arquivoTSE é o recorte do arquivo "-u.json" do TSE que o programa usa.
type arquivoTSE struct {
	Dg  string `json:"dg"`
	Hg  string `json:"hg"`
	Idg string `json:"idg"`
	Tf  string `json:"tf"`
	S   struct {
		Ts   string `json:"ts"`
		St   string `json:"st"`
		Pstn string `json:"pstn"`
	} `json:"s"`
	E struct {
		Te  string `json:"te"`
		Est string `json:"est"`
		C   string `json:"c"`
		Pcn string `json:"pcn"`
		A   string `json:"a"`
		Pan string `json:"pan"`
	} `json:"e"`
	V struct {
		Tv    string `json:"tv"`
		Vv    string `json:"vv"`
		Pvvcn string `json:"pvvcn"`
		Vb    string `json:"vb"`
		Pvbn  string `json:"pvbn"`
		Tvn   string `json:"tvn"`
		Ptvnn string `json:"ptvnn"`
	} `json:"v"`
	// Os candidatos ficam aninhados em cargo > agremiação > partido > candidato.
	Carg []struct {
		Agr []struct {
			Par []struct {
				Sg   string `json:"sg"`
				Cand []struct {
					N     string `json:"n"`
					Nm    string `json:"nm"`
					Nmu   string `json:"nmu"`
					Vap   string `json:"vap"`
					Pvapn string `json:"pvapn"`
					St    string `json:"st"`
					Seq   string `json:"seq"`
				} `json:"cand"`
			} `json:"par"`
		} `json:"agr"`
	} `json:"carg"`
}

type Candidato struct {
	Numero, Nome, Partido, Situacao string
	Votos                           int64
	Pct                             float64 // sobre os votos válidos
	Seq                             int     // classificação informada pelo TSE (muda com as viradas)
}

// Resultado de uma eleição numa abrangência (BR, um estado ou ZZ).
type Resultado struct {
	UF                string
	Atualizacao       string
	Hora              string // hh:mm:ss em que o TSE gerou o arquivo
	Idg               string
	Concluida         bool
	Secoes            int64
	SecoesTotalizadas int64
	PctSecoes         float64
	Eleitorado        int64
	EleitoradoApurado int64 // eleitorado das seções já totalizadas
	Comparecimento    int64
	PctComparecimento float64
	Abstencao         int64
	PctAbstencao      float64
	TotalVotos        int64
	Validos           int64
	PctValidos        float64
	Brancos           int64
	PctBrancos        float64
	Nulos             int64
	PctNulos          float64
	Candidatos        []Candidato // por votos; empate segue a ordem da urna
}

func (r *Resultado) candidato(numero string) (Candidato, bool) {
	for _, c := range r.Candidatos {
		if c.Numero == numero {
			return c, true
		}
	}
	return Candidato{}, false
}

func converter(a *arquivoTSE, uf string) *Resultado {
	r := &Resultado{
		UF:                uf,
		Atualizacao:       strings.TrimSpace(a.Dg + " " + a.Hg),
		Hora:              a.Hg,
		Idg:               a.Idg,
		Concluida:         a.Tf == "s",
		Secoes:            numero(a.S.Ts),
		SecoesTotalizadas: numero(a.S.St),
		PctSecoes:         decimal(a.S.Pstn),
		Eleitorado:        numero(a.E.Te),
		EleitoradoApurado: numero(a.E.Est),
		Comparecimento:    numero(a.E.C),
		PctComparecimento: decimal(a.E.Pcn),
		Abstencao:         numero(a.E.A),
		PctAbstencao:      decimal(a.E.Pan),
		TotalVotos:        numero(a.V.Tv),
		Validos:           numero(a.V.Vv),
		PctValidos:        decimal(a.V.Pvvcn),
		Brancos:           numero(a.V.Vb),
		PctBrancos:        decimal(a.V.Pvbn),
		Nulos:             numero(a.V.Tvn),
		PctNulos:          decimal(a.V.Ptvnn),
	}
	for _, cargo := range a.Carg {
		for _, agr := range cargo.Agr {
			for _, par := range agr.Par {
				for _, c := range par.Cand {
					nome := c.Nmu
					if nome == "" {
						nome = c.Nm
					}
					seq, _ := strconv.Atoi(c.Seq)
					r.Candidatos = append(r.Candidatos, Candidato{
						Numero: c.N, Nome: nome, Partido: par.Sg, Situacao: c.St,
						Votos: numero(c.Vap), Pct: decimal(c.Pvapn), Seq: seq,
					})
				}
			}
		}
	}
	slices.SortStableFunc(r.Candidatos, func(x, y Candidato) int {
		return cmp.Or(cmp.Compare(y.Votos, x.Votos), cmp.Compare(x.Seq, y.Seq))
	})
	return r
}

// Eleicoes guarda o código e o nome da eleição para Presidente de cada turno (índice 1 e 2).
type Eleicoes struct {
	Codigo [3]int
	Nome   [3]string
}

type erroStatus int

func (e erroStatus) Error() string { return fmt.Sprintf("HTTP %d", int(e)) }

func naoEncontrado(err error) bool {
	var s erroStatus
	return errors.As(err, &s) && s == http.StatusNotFound
}

// limitado diz se o servidor recusou a consulta por excesso de pedidos (HTTP 429).
func limitado(err error) bool {
	var s erroStatus
	return errors.As(err, &s) && s == http.StatusTooManyRequests
}

type Cliente struct {
	base string
	http *http.Client
}

func novoCliente(base string) *Cliente {
	// O padrão de 2 conexões ociosas por servidor faria as consultas em paralelo (as cidades
	// de um estado são centenas de arquivos) abrirem conexões novas o tempo todo.
	transporte := http.DefaultTransport.(*http.Transport).Clone()
	transporte.MaxIdleConnsPerHost = consultasCidades
	return &Cliente{base: base, http: &http.Client{Timeout: 20 * time.Second, Transport: transporte}}
}

// urlResultado monta o endereço do arquivo de uma abrangência: "br", a sigla de uma UF ou, para
// uma cidade, a UF seguida do código do município ("sp71072"), que fica na pasta da UF.
func (c *Cliente) urlResultado(ciclo string, eleicao int, chave string) string {
	return fmt.Sprintf("%s/%s/%d/dados/%s/%s-c%04d-e%06d-u.json",
		c.base, ciclo, eleicao, chave[:2], chave, cargoPresidente, eleicao)
}

// tentativas429 é quantas vezes, no total, uma consulta recusada com HTTP 429 é feita. O limite do
// TSE é por segundo e compartilhado por todos que consultam o site (x-ratelimit-limit "5000;w=1"):
// nos picos de acesso, esperar um ou dois segundos costuma bastar.
const tentativas429 = 3

// get baixa url para destino. Com etag, faz uma consulta condicional: quando o arquivo
// não mudou (HTTP 304, sem corpo), devolve mudou == false e não toca em destino.
func (c *Cliente) get(ctx context.Context, url, etag string, destino any) (novaEtag string, mudou bool, err error) {
	for tentativa := 1; ; tentativa++ {
		var espera time.Duration
		novaEtag, mudou, espera, err = c.getUmaVez(ctx, url, etag, destino)
		if !limitado(err) || tentativa == tentativas429 {
			return novaEtag, mudou, err
		}
		select {
		case <-ctx.Done():
			return etag, false, err
		case <-time.After(espera):
		}
	}
}

// getUmaVez faz uma consulta. Num 429, espera diz quanto aguardar antes da próxima: o
// x-ratelimit-reset ou o Retry-After do servidor (em segundos), mais um pouco ao acaso, para as
// consultas em paralelo não voltarem todas juntas.
func (c *Cliente) getUmaVez(ctx context.Context, url, etag string, destino any) (string, bool, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return etag, false, 0, err
	}
	req.Header.Set("User-Agent", "eleicoes-cli/2.0")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return etag, false, 0, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotModified:
		return etag, false, 0, nil
	case http.StatusTooManyRequests:
		segundos, err := strconv.Atoi(cmp.Or(resp.Header.Get("x-ratelimit-reset"), resp.Header.Get("Retry-After")))
		if err != nil {
			segundos = 1
		}
		espera := time.Duration(min(max(segundos, 1), 10))*time.Second + rand.N(time.Second)
		return etag, false, espera, erroStatus(resp.StatusCode)
	default:
		return etag, false, 0, erroStatus(resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(destino); err != nil {
		return etag, false, 0, fmt.Errorf("resposta inválida: %w", err)
	}
	return resp.Header.Get("ETag"), true, 0, nil
}

// eleicaoTSE é uma eleição listada no ele-c.json.
type eleicaoTSE struct {
	Cd  string `json:"cd"`
	Nm  string `json:"nm"`
	T   string `json:"t"`
	Abr []struct {
		Cp []struct {
			Cd string `json:"cd"`
		} `json:"cp"`
	} `json:"abr"`
}

func (e eleicaoTSE) temCargo(cargo int) bool {
	for _, a := range e.Abr {
		for _, cp := range a.Cp {
			if cp.Cd == strconv.Itoa(cargo) {
				return true
			}
		}
	}
	return false
}

// descobrir procura no ele-c.json as eleições do ciclo que têm o cargo de Presidente.
// O 2º turno costuma estar em outro pleito ("pl") que não o do 1º, por isso percorre todos.
func (c *Cliente) descobrir(ctx context.Context, ciclo string) (Eleicoes, error) {
	var cfg struct {
		Pl []struct {
			C string       `json:"c"`
			E []eleicaoTSE `json:"e"`
		} `json:"pl"`
	}
	if _, _, err := c.get(ctx, c.base+"/comum/config/ele-c.json", "", &cfg); err != nil {
		return Eleicoes{}, err
	}
	var r Eleicoes
	for _, pl := range cfg.Pl {
		if pl.C != ciclo {
			continue
		}
		for _, e := range pl.E {
			turno, _ := strconv.Atoi(e.T)
			if turno < 1 || turno > 2 || r.Codigo[turno] != 0 || !e.temCargo(cargoPresidente) {
				continue
			}
			r.Codigo[turno], _ = strconv.Atoi(e.Cd)
			r.Nome[turno] = e.Nm
		}
	}
	if r.Codigo[1] == 0 && r.Codigo[2] == 0 {
		return r, fmt.Errorf("nenhuma eleição para Presidente no ciclo %s", ciclo)
	}
	return r, nil
}

type alvo struct {
	turno int
	uf    string // "br", sigla da UF ou chave de uma cidade ("sp71072")
}

type item struct {
	alvo  alvo
	url   string
	dados *Resultado // nil quando não mudou (304) ou deu erro
	etag  string
	err   error
}

// rodada é o resultado de uma consulta a todos os arquivos.
type rodada struct {
	eleicoes *Eleicoes // preenchido quando a descoberta acontece nesta rodada
	err      error     // falha ao ler a configuração
	itens    []item
}

// buscarTudo consulta BR, estados e ZZ dos dois turnos (o 1º pode ser pulado quando já
// está completo). etags não é alterado: as novas ETags voltam nos itens.
func (c *Cliente) buscarTudo(ctx context.Context, ciclo string, ele *Eleicoes, etags map[string]string, pular1 bool) rodada {
	var r rodada
	if ele == nil {
		e, err := c.descobrir(ctx, ciclo)
		if err != nil {
			r.err = err
			return r
		}
		ele, r.eleicoes = &e, &e
	}
	for turno := 1; turno <= 2; turno++ {
		if ele.Codigo[turno] == 0 || (turno == 1 && pular1) {
			continue
		}
		for _, uf := range append([]string{"br"}, ufs...) {
			url := c.urlResultado(ciclo, ele.Codigo[turno], uf)
			r.itens = append(r.itens, item{alvo: alvo{turno, uf}, url: url, etag: etags[url]})
		}
	}
	c.baixar(ctx, r.itens, consultasJuntas)
	return r
}

// baixar consulta os itens, com no máximo juntas consultas ao mesmo tempo, e preenche em cada
// um os dados, a nova ETag ou o erro.
func (c *Cliente) baixar(ctx context.Context, itens []item, juntas int) {
	var wg sync.WaitGroup
	vagas := make(chan struct{}, juntas)
	for i := range itens {
		wg.Go(func() {
			vagas <- struct{}{}
			defer func() { <-vagas }()
			it := &itens[i]
			var a arquivoTSE
			etag, mudou, err := c.get(ctx, it.url, it.etag, &a)
			it.etag, it.err = etag, err
			if mudou {
				it.dados = converter(&a, it.alvo.uf)
			}
		})
	}
	wg.Wait()
}
