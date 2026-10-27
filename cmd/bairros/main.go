// Comando bairros monta o resultado para Presidente por bairro com os dados abertos do TSE.
//
// O TSE não publica o resultado por bairro. Ele é montado com três arquivos nacionais do Portal
// de Dados Abertos: os votos de cada candidato por seção (votação por seção), os aptos,
// comparecimento, brancos e nulos por seção (detalhe da apuração por seção) e o bairro do local
// de votação de cada seção (eleitorado por local de votação). As seções são somadas pelo bairro
// do local onde votam, que não é necessariamente o bairro onde o eleitor mora.
//
// Grava um JSON por UF (dados/bairros/sp.json…), que o programa eleicoes mostra na tela Cidades
// com a tecla b. Os turnos entram à medida que o TSE os publica: basta rodar de novo, e só os
// arquivos que mudaram no TSE são baixados outra vez.
package main

import (
	"archive/zip"
	"bufio"
	"cmp"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	cdn        = "https://cdn.tse.jus.br/estatistica/sead/odsele"
	resultados = "https://resultados.tse.jus.br/oficial"
)

// fonte é um arquivo do Portal de Dados Abertos; o caminho leva o ano.
type fonte struct {
	nome, caminho string
}

var (
	fonteLocais  = fonte{"eleitorado por local de votação", "eleitorado_locais_votacao/eleitorado_local_votacao_%d.zip"}
	fonteDetalhe = fonte{"detalhe da apuração por seção", "detalhe_votacao_secao/detalhe_votacao_secao_%d.zip"}
	fonteVotacao = fonte{"votação por seção", "votacao_secao/votacao_secao_%d_BR.zip"}
)

// secao identifica uma seção num turno. O código do município do TSE é único no país.
type secao struct {
	turno        int
	municipio    int
	zona, numero int
}

// Totais de um bairro num turno.
type Totais struct {
	Secoes         int              `json:"secoes"`
	Locais         int              `json:"locais"`
	Aptos          int64            `json:"aptos"`
	Comparecimento int64            `json:"comparecimento"`
	Abstencoes     int64            `json:"abstencoes"`
	Nominais       int64            `json:"nominais"`
	Brancos        int64            `json:"brancos"`
	Nulos          int64            `json:"nulos"`
	Votos          map[string]int64 `json:"votos"` // por número do candidato

	locais map[[2]int]bool // zona e número do local de votação
}

// Bairro é uma linha do JSON: o nome mais frequente no cadastro e os totais de cada turno.
type Bairro struct {
	Nome   string          `json:"nome"`
	Turnos map[int]*Totais `json:"turnos"`

	grafias map[string]int // nomes como aparecem no cadastro, com o número de seções
}

// Saida é o JSON de uma UF.
type Saida struct {
	UF          string                        `json:"uf"`
	Gerado      string                        `json:"gerado"`
	Fontes      map[string]string             `json:"fontes"`      // nome -> Last-Modified no TSE
	Turnos      []int                         `json:"turnos"`      // turnos com votos
	DataTurno   map[int]string                `json:"dataTurno"`   // geração da votação por seção
	DataEleicao map[int]string                `json:"dataEleicao"` // dia da votação de cada turno
	Candidatos  map[int]map[string]string     `json:"candidatos"`
	Cidades     map[string][]*Bairro          `json:"cidades"` // código do município (5 dígitos)
	SemBairro   map[string]int                `json:"semBairro,omitempty"`
	bairros     map[string]map[string]*Bairro // município -> chave de agrupamento -> bairro
}

func main() {
	ano := flag.Int("ano", 2026, "ano da eleição")
	cache := flag.String("cache", "dados/cache", "pasta onde ficam os arquivos baixados do TSE")
	saida := flag.String("saida", "dados/bairros", "pasta dos JSON por UF")
	teste2 := flag.Bool("teste-2turno", false, "só para testes: repete o 1º turno como se fosse o 2º")
	flag.Parse()
	if err := executar(*ano, *cache, *saida, *teste2); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func executar(ano int, cache, pastaSaida string, teste2 bool) error {
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return err
	}
	cli := &http.Client{Timeout: 30 * time.Minute}
	arquivos := map[fonte]string{}
	fontes := map[string]string{}
	for _, f := range []fonte{fonteLocais, fonteDetalhe, fonteVotacao} {
		url := cdn + "/" + fmt.Sprintf(f.caminho, ano)
		destino := filepath.Join(cache, filepath.Base(url))
		modificado, err := baixar(cli, url, destino)
		if err != nil {
			return fmt.Errorf("%s: %w", f.nome, err)
		}
		arquivos[f], fontes[f.nome] = destino, modificado
		fmt.Printf("%-32s %s (TSE: %s)\n", f.nome, filepath.Base(url), modificado)
	}
	if teste2 {
		fmt.Println("ATENÇÃO: -teste-2turno repete o 1º turno como 2º; os dados gerados são só para testes.")
	}

	inicio := time.Now()
	locais, err := lerLocais(arquivos[fonteLocais])
	if err != nil {
		return err
	}
	saidas := map[string]*Saida{}
	semLocal := map[string]int{}
	// acumulador devolve os totais do bairro da seção no turno, criando o que faltar, e a grafia
	// do bairro no cadastro.
	acumulador := func(s secao, uf string) (*Saida, *Bairro, *Totais, string) {
		nome, ok := locais[s]
		if !ok {
			// Turno sem a seção no cadastro: usa o bairro da mesma seção no outro turno.
			nome, ok = locais[secao{3 - s.turno, s.municipio, s.zona, s.numero}]
		}
		sd := saidas[uf]
		if sd == nil {
			sd = &Saida{UF: strings.ToLower(uf), Fontes: fontes, DataTurno: map[int]string{}, DataEleicao: map[int]string{},
				Candidatos: map[int]map[string]string{}, Cidades: map[string][]*Bairro{},
				bairros: map[string]map[string]*Bairro{}}
			saidas[uf] = sd
		}
		mun := fmt.Sprintf("%05d", s.municipio)
		switch {
		case !ok:
			nome = "(SEÇÃO FORA DO CADASTRO DE LOCAIS)"
			semLocal[uf]++
			if sd.SemBairro == nil {
				sd.SemBairro = map[string]int{}
			}
			sd.SemBairro[mun]++
		case nome == "":
			nome = "(SEM BAIRRO NO CADASTRO)"
		}
		chave := agrupar(nome)
		porMun := sd.bairros[mun]
		if porMun == nil {
			porMun = map[string]*Bairro{}
			sd.bairros[mun] = porMun
		}
		b := porMun[chave]
		if b == nil {
			b = &Bairro{Turnos: map[int]*Totais{}, grafias: map[string]int{}}
			porMun[chave] = b
		}
		t := b.Turnos[s.turno]
		if t == nil {
			t = &Totais{Votos: map[string]int64{}, locais: map[[2]int]bool{}}
			b.Turnos[s.turno] = t
		}
		return sd, b, t, nome
	}
	// Os turnos repetidos por -teste-2turno.
	turnos := func(t int) []int {
		if teste2 && t == 1 {
			return []int{1, 2}
		}
		return []int{t}
	}

	// Detalhe: só o arquivo nacional de Presidente (_BR). O _BRASIL repete as mesmas seções.
	nDetalhe := 0
	turnosDetalhe := map[int]bool{}
	err = percorrer(arquivos[fonteDetalhe], func(nome string) bool { return strings.HasSuffix(nome, "_BR.csv") },
		func(c colunas) (func([]string) error, error) {
			idx, err := c.indices("NR_TURNO", "CD_MUNICIPIO", "NR_ZONA", "NR_SECAO", "CD_CARGO", "QT_APTOS",
				"QT_COMPARECIMENTO", "QT_ABSTENCOES", "QT_VOTOS_NOMINAIS", "QT_VOTOS_BRANCOS", "QT_VOTOS_NULOS",
				"QT_VOTOS_LEGENDA", "QT_VOTOS_ANULADOS_APU_SEP", "NR_LOCAL_VOTACAO", "SG_UF")
			if err != nil {
				return nil, err
			}
			return func(r []string) error {
				if r[idx[4]] != "1" {
					return nil
				}
				nDetalhe++
				for _, turno := range turnos(inteiro(r[idx[0]])) {
					turnosDetalhe[turno] = true
					s := secao{turno, inteiro(r[idx[1]]), inteiro(r[idx[2]]), inteiro(r[idx[3]])}
					_, b, t, nome := acumulador(s, r[idx[14]])
					b.grafias[nome]++ // uma linha por seção: conta a grafia pelo número de seções
					t.Secoes++
					t.locais[[2]int{s.zona, inteiro(r[idx[13]])}] = true
					t.Aptos += int64(inteiro(r[idx[5]]))
					t.Comparecimento += int64(inteiro(r[idx[6]]))
					t.Abstencoes += int64(inteiro(r[idx[7]]))
					// Votos de legenda não existem para Presidente; somados por garantia.
					t.Nominais += int64(inteiro(r[idx[8]]) + inteiro(r[idx[11]]))
					t.Brancos += int64(inteiro(r[idx[9]]))
					t.Nulos += int64(inteiro(r[idx[10]]) + inteiro(r[idx[12]]))
				}
				return nil
			}, nil
		})
	if err != nil {
		return fmt.Errorf("%s: %w", fonteDetalhe.nome, err)
	}

	nVotacao := 0
	eleicoes := map[int]string{} // código da eleição de cada turno, para a lista oficial de candidatos
	err = percorrer(arquivos[fonteVotacao], func(string) bool { return true },
		func(c colunas) (func([]string) error, error) {
			idx, err := c.indices("NR_TURNO", "CD_MUNICIPIO", "NR_ZONA", "NR_SECAO", "CD_CARGO",
				"NR_VOTAVEL", "NM_VOTAVEL", "QT_VOTOS", "DT_GERACAO", "HH_GERACAO", "SG_UF", "CD_ELEICAO", "DT_ELEICAO")
			if err != nil {
				return nil, err
			}
			return func(r []string) error {
				numero := r[idx[5]]
				// 95 a 98 são brancos, nulos e anulados, que vêm do detalhe.
				if r[idx[4]] != "1" || (len(numero) == 2 && numero >= "95" && numero <= "98") {
					return nil
				}
				nVotacao++
				for _, turno := range turnos(inteiro(r[idx[0]])) {
					sd, _, t, _ := acumulador(secao{turno, inteiro(r[idx[1]]), inteiro(r[idx[2]]), inteiro(r[idx[3]])}, r[idx[10]])
					t.Votos[numero] += int64(inteiro(r[idx[7]]))
					if sd.Candidatos[turno] == nil {
						sd.Candidatos[turno] = map[string]string{}
					}
					if _, ok := sd.Candidatos[turno][numero]; !ok {
						sd.Candidatos[turno][numero] = latin1(r[idx[6]])
					}
					if sd.DataTurno[turno] == "" {
						sd.DataTurno[turno] = r[idx[8]] + " " + r[idx[9]]
						sd.DataEleicao[turno] = r[idx[12]]
					}
					eleicoes[turno] = r[idx[11]]
				}
				return nil
			}, nil
		})
	if err != nil {
		return fmt.Errorf("%s: %w", fonteVotacao.nome, err)
	}
	fmt.Printf("lidos: %d seções no detalhe e %d linhas de votos para Presidente em %s\n",
		nDetalhe, nVotacao, time.Since(inicio).Round(time.Second))
	if len(semLocal) > 0 {
		fmt.Printf("seções fora do cadastro de locais, por UF: %v\n", semLocal)
	}
	descartarIncompletos(turnosDetalhe, eleicoes, saidas)
	if err := anularInvalidos(cli, ano, eleicoes, saidas); err != nil {
		return err
	}

	if err := os.MkdirAll(pastaSaida, 0o755); err != nil {
		return err
	}
	gerado := time.Now().Format("02/01/2006 15:04:05")
	for _, uf := range slices.Sorted(maps.Keys(saidas)) {
		sd := saidas[uf]
		sd.Gerado = gerado
		var nBairros int
		for mun, porMun := range sd.bairros {
			for _, b := range porMun {
				b.Nome = grafiaMaisComum(b.grafias)
				for _, t := range b.Turnos {
					t.Locais = len(t.locais)
				}
				sd.Cidades[mun] = append(sd.Cidades[mun], b)
			}
			slices.SortFunc(sd.Cidades[mun], func(a, b *Bairro) int { return strings.Compare(agrupar(a.Nome), agrupar(b.Nome)) })
			nBairros += len(porMun)
		}
		presentes := map[int]bool{}
		for _, bs := range sd.Cidades {
			for _, b := range bs {
				for t := range b.Turnos {
					presentes[t] = true
				}
			}
		}
		sd.Turnos = slices.Sorted(maps.Keys(presentes))
		caminho := filepath.Join(pastaSaida, sd.UF+".json")
		if err := gravarJSON(caminho, sd); err != nil {
			return err
		}
		fmt.Printf("%s: %d cidades, %d bairros, turnos %v\n", caminho, len(sd.Cidades), nBairros, sd.Turnos)
	}
	return nil
}

// descartarIncompletos tira os turnos que estão em só um dos dois arquivos de votos. O TSE publica
// o detalhe e a votação por seção em dias diferentes (no 2º turno de 2026, o detalhe saiu antes),
// e um turno pela metade entraria com comparecimento e nenhum voto em candidato, ou o contrário.
func descartarIncompletos(detalhe map[int]bool, eleicoes map[int]string, saidas map[string]*Saida) {
	todos := maps.Clone(detalhe)
	for turno := range eleicoes {
		todos[turno] = true
	}
	for _, turno := range slices.Sorted(maps.Keys(todos)) {
		_, temVotos := eleicoes[turno]
		if detalhe[turno] && temVotos {
			continue
		}
		falta := fonteVotacao
		if !detalhe[turno] {
			falta = fonteDetalhe
		}
		fmt.Printf("%dº turno fica de fora até o TSE publicar o arquivo de %s desse turno\n", turno, falta.nome)
		delete(eleicoes, turno)
		for _, sd := range saidas {
			delete(sd.Candidatos, turno)
			delete(sd.DataTurno, turno)
			delete(sd.DataEleicao, turno)
			for mun, porMun := range sd.bairros {
				for chave, b := range porMun {
					delete(b.Turnos, turno)
					if len(b.Turnos) == 0 {
						delete(porMun, chave)
					}
				}
				if len(porMun) == 0 {
					delete(sd.bairros, mun)
				}
			}
		}
	}
}

// oficial é o resultado nacional de um turno no site de resultados do TSE.
type oficial struct {
	votos                                      map[string]int64 // por candidato válido
	brancos, nulos, comparecimento, abstencoes int64
}

// anularInvalidos passa para os nulos os votos de candidatos que não estão no resultado oficial do
// turno (candidaturas anuladas ou indeferidas): os arquivos por seção trazem esses votos como
// nominais, mas o TSE os conta como nulos. Depois confere a soma do país com o resultado oficial,
// para um turno novo não entrar com alguma diferença que passe despercebida.
func anularInvalidos(cli *http.Client, ano int, eleicoes map[int]string, saidas map[string]*Saida) error {
	for _, turno := range slices.Sorted(maps.Keys(eleicoes)) {
		cod, _ := strconv.Atoi(eleicoes[turno])
		url := fmt.Sprintf("%s/ele%d/%d/dados/br/br-c0001-e%06d-u.json", resultados, ano, cod, cod)
		of, err := resultadoOficial(cli, url)
		if err != nil {
			return fmt.Errorf("resultado oficial do %dº turno: %w", turno, err)
		}
		anulados := map[string]int64{}
		soma := oficial{votos: map[string]int64{}}
		for _, sd := range saidas {
			for _, porMun := range sd.bairros {
				for _, b := range porMun {
					t := b.Turnos[turno]
					if t == nil {
						continue
					}
					for numero, q := range t.Votos {
						if _, ok := of.votos[numero]; !ok {
							t.Nominais -= q
							t.Nulos += q
							delete(t.Votos, numero)
							anulados[numero] += q
						}
					}
					for numero, q := range t.Votos {
						soma.votos[numero] += q
					}
					soma.brancos += t.Brancos
					soma.nulos += t.Nulos
					soma.comparecimento += t.Comparecimento
					soma.abstencoes += t.Abstencoes
				}
			}
			for numero := range sd.Candidatos[turno] {
				if _, ok := of.votos[numero]; !ok {
					delete(sd.Candidatos[turno], numero)
				}
			}
		}
		if len(anulados) > 0 {
			fmt.Printf("%dº turno: votos de candidatos fora do resultado oficial, contados como nulos: %v\n", turno, anulados)
		}
		if difs := diferencas(soma, of); len(difs) > 0 {
			fmt.Printf("ATENÇÃO, %dº turno: a soma dos bairros NÃO confere com o resultado oficial do TSE (bairros ≠ TSE): %s\n",
				turno, strings.Join(difs, "; "))
		} else {
			fmt.Printf("%dº turno: a soma dos bairros confere com o resultado oficial do TSE (candidatos, brancos, nulos, comparecimento e abstenção)\n", turno)
		}
	}
	return nil
}

func diferencas(soma, of oficial) []string {
	var difs []string
	compara := func(nome string, a, b int64) {
		if a != b {
			difs = append(difs, fmt.Sprintf("%s %d ≠ %d", nome, a, b))
		}
	}
	for _, numero := range slices.Sorted(maps.Keys(of.votos)) {
		compara("nº "+numero, soma.votos[numero], of.votos[numero])
	}
	compara("brancos", soma.brancos, of.brancos)
	compara("nulos", soma.nulos, of.nulos)
	compara("comparecimento", soma.comparecimento, of.comparecimento)
	compara("abstenção", soma.abstencoes, of.abstencoes)
	return difs
}

// resultadoOficial lê o arquivo nacional do turno. Na noite de apuração, o TSE recusa consultas
// com HTTP 429 de vez em quando: tenta de novo algumas vezes antes de desistir.
func resultadoOficial(cli *http.Client, url string) (oficial, error) {
	var resp *http.Response
	var err error
	for tentativa := 1; ; tentativa++ {
		resp, err = cli.Get(url)
		if err != nil {
			return oficial{}, err
		}
		if resp.StatusCode != http.StatusTooManyRequests || tentativa == 5 {
			break
		}
		resp.Body.Close()
		time.Sleep(time.Duration(tentativa) * 2 * time.Second)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return oficial{}, fmt.Errorf("HTTP %d em %s", resp.StatusCode, url)
	}
	var r struct {
		E struct{ C, A string } `json:"e"`
		V struct {
			Vb  string `json:"vb"`
			Tvn string `json:"tvn"`
		} `json:"v"`
		Carg []struct {
			Agr []struct {
				Par []struct {
					Cand []struct {
						N   string `json:"n"`
						Vap string `json:"vap"`
					} `json:"cand"`
				} `json:"par"`
			} `json:"agr"`
		} `json:"carg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return oficial{}, err
	}
	of := oficial{votos: map[string]int64{}, brancos: int64(inteiro(r.V.Vb)), nulos: int64(inteiro(r.V.Tvn)),
		comparecimento: int64(inteiro(r.E.C)), abstencoes: int64(inteiro(r.E.A))}
	for _, c := range r.Carg {
		for _, a := range c.Agr {
			for _, p := range a.Par {
				for _, cand := range p.Cand {
					of.votos[cand.N] = int64(inteiro(cand.Vap))
				}
			}
		}
	}
	if len(of.votos) == 0 {
		return oficial{}, fmt.Errorf("nenhum candidato em %s", url)
	}
	return of, nil
}

// lerLocais mapeia cada seção, em cada turno, para o bairro do seu local de votação.
func lerLocais(caminho string) (map[secao]string, error) {
	locais := map[secao]string{}
	nomes := map[string]string{} // evita guardar o mesmo nome de bairro milhares de vezes
	// O arquivo _BRASIL repete todos os estaduais.
	err := percorrer(caminho, func(nome string) bool { return !strings.Contains(nome, "_BRASIL") },
		func(c colunas) (func([]string) error, error) {
			idx, err := c.indices("NR_TURNO", "CD_MUNICIPIO", "NR_ZONA", "NR_SECAO", "NM_BAIRRO")
			if err != nil {
				return nil, err
			}
			return func(r []string) error {
				bruto := strings.TrimSpace(r[idx[4]])
				nome, ok := nomes[bruto]
				if !ok {
					nome = strings.Join(strings.Fields(latin1(bruto)), " ")
					nomes[bruto] = nome
				}
				locais[secao{inteiro(r[idx[0]]), inteiro(r[idx[1]]), inteiro(r[idx[2]]), inteiro(r[idx[3]])}] = nome
				return nil
			}, nil
		})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fonteLocais.nome, err)
	}
	fmt.Printf("cadastro de locais: %d seções (somando os turnos)\n", len(locais))
	return locais, nil
}

// grafiaMaisComum escolhe, entre as grafias de um bairro no cadastro, a de mais seções.
func grafiaMaisComum(grafias map[string]int) string {
	melhor, n := "", -1
	for _, g := range slices.Sorted(maps.Keys(grafias)) {
		if grafias[g] > n {
			melhor, n = g, grafias[g]
		}
	}
	return melhor
}

// agrupar junta grafias do mesmo bairro: sem acentos, maiúsculas, sem pontuação e espaços repetidos.
func agrupar(s string) string {
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
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '(' || r == ')' {
			return r
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

type colunas map[string]int

func (c colunas) indices(nomes ...string) ([]int, error) {
	idx := make([]int, len(nomes))
	for i, n := range nomes {
		j, ok := c[n]
		if !ok {
			return nil, fmt.Errorf("coluna %s não encontrada", n)
		}
		idx[i] = j
	}
	return idx, nil
}

// percorrer lê os CSVs aceitos dentro do zip (latin1, separados por ";"). Para cada arquivo,
// preparar recebe o cabeçalho e devolve a função chamada em cada linha.
func percorrer(caminho string, aceitar func(string) bool, preparar func(colunas) (func([]string) error, error)) error {
	z, err := zip.OpenReader(caminho)
	if err != nil {
		return err
	}
	defer z.Close()
	lidos := 0
	for _, f := range z.File {
		if !strings.HasSuffix(f.Name, ".csv") || !aceitar(f.Name) {
			continue
		}
		if err := percorrerCSV(f, preparar); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
		lidos++
	}
	if lidos == 0 {
		return errors.New("nenhum CSV esperado dentro do zip")
	}
	return nil
}

func percorrerCSV(f *zip.File, preparar func(colunas) (func([]string) error, error)) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	cr := csv.NewReader(bufio.NewReaderSize(rc, 1<<20))
	cr.Comma, cr.ReuseRecord, cr.LazyQuotes, cr.FieldsPerRecord = ';', true, true, -1
	cab, err := cr.Read()
	if err != nil {
		return err
	}
	c := colunas{}
	for i, nome := range cab {
		c[strings.TrimPrefix(nome, "\ufeff")] = i
	}
	linha, err := preparar(c)
	if err != nil {
		return err
	}
	for {
		r, err := cr.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := linha(r); err != nil {
			return err
		}
	}
}

func inteiro(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// latin1 converte os textos do TSE, gravados em ISO-8859-1, para UTF-8.
func latin1(s string) string {
	r := make([]rune, len(s))
	for i := range len(s) {
		r[i] = rune(s[i])
	}
	return string(r)
}

// baixar guarda o arquivo em cache e só o baixa de novo quando o Last-Modified do TSE muda.
func baixar(cli *http.Client, url, destino string) (string, error) {
	// O CDN do TSE segue entregando a versão anterior por horas depois de uma publicação e ignora
	// o Cache-Control do pedido. Com um parâmetro que muda a cada execução, a resposta vem da origem.
	origem := fmt.Sprintf("%s?v=%d", url, time.Now().UnixNano())
	resp, err := cli.Head(origem)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d em %s", resp.StatusCode, url)
	}
	modificado := resp.Header.Get("Last-Modified")
	marca := destino + ".modificado"
	if anterior, err := os.ReadFile(marca); err == nil {
		if _, err := os.Stat(destino); err == nil {
			if string(anterior) == modificado {
				return modificado, nil
			}
			// Por garantia, o cache nunca é trocado por uma versão mais antiga, como as que o CDN
			// entregava antes da consulta à origem.
			novo, err1 := http.ParseTime(modificado)
			antigo, err2 := http.ParseTime(string(anterior))
			if err1 == nil && err2 == nil && novo.Before(antigo) {
				fmt.Printf("%s: o TSE respondeu com uma versão mais antiga (%s) que a do cache; fica a do cache\n",
					filepath.Base(url), modificado)
				return string(anterior), nil
			}
		}
	}

	resp, err = cli.Get(origem)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d em %s", resp.StatusCode, url)
	}
	parcial := destino + ".parcial"
	arq, err := os.Create(parcial)
	if err != nil {
		return "", err
	}
	p := &progresso{nome: filepath.Base(url), total: resp.ContentLength}
	_, err = io.Copy(arq, io.TeeReader(resp.Body, p))
	fmt.Fprintln(os.Stderr)
	if err := cmp.Or(err, arq.Close()); err != nil {
		return "", err
	}
	if err := os.Rename(parcial, destino); err != nil {
		return "", err
	}
	return modificado, os.WriteFile(marca, []byte(modificado), 0o644)
}

type progresso struct {
	nome         string
	total, feito int64
	ultimoDecimo int64
}

func (p *progresso) Write(b []byte) (int, error) {
	p.feito += int64(len(b))
	if p.total > 0 {
		if decimo := 10 * p.feito / p.total; decimo != p.ultimoDecimo || p.feito == p.total {
			p.ultimoDecimo = decimo
			fmt.Fprintf(os.Stderr, "\rbaixando %s: %d%% (%d de %d MB)", p.nome, 100*p.feito/p.total, p.feito>>20, p.total>>20)
		}
	}
	return len(b), nil
}

func gravarJSON(caminho string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := caminho + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, caminho)
}
