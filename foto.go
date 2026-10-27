package main

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Tecla f: salva a tela atual como PNG em fotos/ e copia a imagem para a área de transferência,
// para compartilhar. A foto sai limpa, como no modo apresentação: sem a linha de atalhos e sem a
// linha selecionada. A imagem é desenhada aqui mesmo, célula por célula, a partir do texto com as
// cores ANSI que o programa já gera para o terminal.

const (
	pastaFotos       = "fotos"
	tamanhoFonteFoto = 20 // px: com 160 colunas, a foto tem 1.920 px de largura, como a live
)

// msgSalvo avisa que uma foto (tecla f) ou uma tabela (tecla e, em exportar.go) foi gravada.
type msgSalvo struct {
	oque    string // "foto" ou "tabela"
	caminho string
	detalhe string
	copiada bool
	err     error
}

// tirarFoto guarda o texto da tela agora e desenha a imagem fora do Update.
func (m *modelo) tirarFoto() tea.Cmd {
	m.fotografando = true
	tela := m.renderTela()
	m.fotografando = false
	fundo, texto := m.coresPadrao()
	return func() tea.Msg {
		img := desenharTela(tela, fundo, texto)
		caminho := filepath.Join(pastaFotos, "eleicoes-"+time.Now().Format("2006-01-02-15h04m05s")+".png")
		if err := salvarPNG(caminho, img); err != nil {
			return msgSalvo{oque: "foto", err: err}
		}
		return msgSalvo{oque: "foto", caminho: caminho, copiada: copiarImagem(caminho)}
	}
}

func (m *modelo) aplicarSalvo(msg msgSalvo) {
	m.mensagemAte = time.Now().Add(8 * time.Second)
	switch {
	case msg.err != nil:
		m.mensagem = "não foi possível salvar a " + msg.oque + ": " + msg.err.Error()
		return
	case msg.copiada:
		m.mensagem = msg.oque + " salva em " + msg.caminho + " e copiada (ctrl+v para colar)"
	default:
		m.mensagem = msg.oque + " salva em " + msg.caminho
	}
	if msg.detalhe != "" {
		m.mensagem += " (" + msg.detalhe + ")"
	}
}

// coresPadrao são o fundo e o texto do terminal, para as células sem cor própria. Quando o
// terminal não informa (o tmux, por exemplo, não responde), usa as cores da paleta do programa.
func (m *modelo) coresPadrao() (fundo, texto color.Color) {
	fundo, texto = m.corFundo, m.corTexto
	if fundo == nil {
		fundo = m.est.fundoPadrao
	}
	if texto == nil {
		texto = m.est.textoPadrao
	}
	return fundo, texto
}

func salvarPNG(caminho string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		return err
	}
	arq, err := os.Create(caminho)
	if err != nil {
		return err
	}
	if err := png.Encode(arq, img); err != nil {
		arq.Close()
		return err
	}
	return arq.Close()
}

// copiarImagem põe o PNG na área de transferência com wl-copy (Wayland) ou xclip (X11). Sem
// nenhum dos dois, ou com ELEICOES_SEM_COPIA=1 (testes), a foto fica só no arquivo.
func copiarImagem(caminho string) bool {
	if os.Getenv("ELEICOES_SEM_COPIA") != "" {
		return false
	}
	var cmd []string
	switch {
	case os.Getenv("WAYLAND_DISPLAY") != "":
		cmd = []string{"wl-copy", "--type", "image/png"}
	case os.Getenv("DISPLAY") != "":
		cmd = []string{"xclip", "-selection", "clipboard", "-t", "image/png", "-i"}
	default:
		return false
	}
	arq, err := os.Open(caminho)
	if err != nil {
		return false
	}
	defer arq.Close()
	ctx, cancelar := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelar()
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	// Saídas nulas: com writers ligados, o Wait esperaria o processo que essas ferramentas deixam
	// em segundo plano servindo a área de transferência, e ele herdaria o terminal do programa.
	c.Stdin = arq
	return c.Run() == nil
}

// fontes guarda as fontes da foto: a monospace do sistema (a mesma do terminal, em geral), a
// DejaVu e, por último, a Go Mono embutida, para nunca faltar. Cada caractere usa a primeira
// fonte que o tem.
type fontes struct {
	normal, negrito []font.Face
	largura, altura int // da célula, em px
	base            int // da linha de base, a partir do topo da célula
}

var (
	carregarFontes = sync.OnceValue(func() *fontes {
		f := &fontes{}
		abrir := func(caminho string) font.Face {
			b, err := os.ReadFile(caminho)
			if err != nil {
				return nil
			}
			return face(b)
		}
		sistema := func(padrao string) string {
			out, err := exec.Command("fc-match", "-f", "%{file}", padrao).Output()
			if err != nil {
				return ""
			}
			return string(out)
		}
		for _, c := range []string{sistema("monospace"), "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"} {
			if fc := abrir(c); fc != nil {
				f.normal = append(f.normal, fc)
			}
		}
		for _, c := range []string{sistema("monospace:bold"), "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf"} {
			if fc := abrir(c); fc != nil {
				f.negrito = append(f.negrito, fc)
			}
		}
		f.normal = append(f.normal, face(gomono.TTF))
		f.negrito = append(f.negrito, face(gomonobold.TTF))

		principal := f.normal[0]
		avanco, _ := principal.GlyphAdvance('0')
		met := principal.Metrics()
		f.largura = avanco.Round()
		f.altura = (met.Ascent + met.Descent).Ceil() + 2
		f.base = 1 + met.Ascent.Ceil()
		return f
	})
)

func face(ttf []byte) font.Face {
	fnt, err := opentype.Parse(ttf)
	if err != nil {
		return nil
	}
	fc, err := opentype.NewFace(fnt, &opentype.FaceOptions{Size: tamanhoFonteFoto, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil
	}
	return fc
}

// faceDe escolhe a primeira fonte que tem o caractere; nil quando nenhuma tem.
func faceDe(faces []font.Face, r rune) font.Face {
	for _, fc := range faces {
		if fc == nil {
			continue
		}
		if _, ok := fc.GlyphAdvance(r); ok {
			return fc
		}
	}
	return nil
}

// desenharTela transforma o texto com cores ANSI numa imagem, com as linhas vazias do fim
// cortadas e uma margem em volta.
func desenharTela(tela string, fundo, texto color.Color) *image.RGBA {
	linhas := strings.Split(strings.TrimRight(tela, "\n"), "\n")
	for len(linhas) > 1 && strings.TrimSpace(ansi.Strip(linhas[len(linhas)-1])) == "" {
		linhas = linhas[:len(linhas)-1]
	}
	colunas := 1
	for _, l := range linhas {
		colunas = max(colunas, ansi.StringWidth(l))
	}
	buf := uv.NewScreenBuffer(colunas, len(linhas))
	uv.NewStyledString(strings.Join(linhas, "\n")).Draw(buf, uv.Rect(0, 0, colunas, len(linhas)))

	f := carregarFontes()
	margem := f.altura / 2
	img := image.NewRGBA(image.Rect(0, 0, colunas*f.largura+2*margem, len(linhas)*f.altura+2*margem))
	draw.Draw(img, img.Bounds(), image.NewUniform(fundo), image.Point{}, draw.Src)

	for y := range len(linhas) {
		for x := 0; x < colunas; x++ {
			c := buf.CellAt(x, y)
			if c == nil || c.Width == 0 {
				continue // vazia ou continuação de um caractere largo
			}
			frente, tras := texto, fundo
			if c.Style.Fg != nil {
				frente = c.Style.Fg
			}
			if c.Style.Bg != nil {
				tras = c.Style.Bg
			}
			if c.Style.Attrs&uv.AttrReverse != 0 {
				frente, tras = tras, frente
			}
			if c.Style.Attrs&uv.AttrFaint != 0 {
				frente = misturar(frente, tras, 0.5)
			}
			ret := image.Rect(margem+x*f.largura, margem+y*f.altura, margem+(x+c.Width)*f.largura, margem+(y+1)*f.altura)
			if c.Style.Bg != nil || c.Style.Attrs&uv.AttrReverse != 0 {
				draw.Draw(img, ret, image.NewUniform(tras), image.Point{}, draw.Src)
			}
			if !desenharEspecial(img, ret, c.Content, frente, tras) && strings.TrimSpace(c.Content) != "" {
				faces := f.normal
				if c.Style.Attrs&uv.AttrBold != 0 {
					faces = f.negrito
				}
				r := []rune(c.Content)[0]
				if fc := faceDe(faces, r); fc != nil {
					d := font.Drawer{Dst: img, Src: image.NewUniform(frente), Face: fc,
						Dot: fixed.P(ret.Min.X, ret.Min.Y+f.base)}
					d.DrawString(c.Content)
				}
			}
			if c.Style.Underline != uv.UnderlineNone {
				y0 := ret.Min.Y + f.base + 2
				draw.Draw(img, image.Rect(ret.Min.X, y0, ret.Max.X, y0+1), image.NewUniform(frente), image.Point{}, draw.Src)
			}
		}
	}
	return img
}

// desenharEspecial desenha as linhas da tabela e os blocos das barras como formas, ocupando a
// célula inteira, para as linhas verticais não ficarem com falhas entre uma linha e outra.
func desenharEspecial(img *image.RGBA, r image.Rectangle, s string, frente, tras color.Color) bool {
	preencher := func(ret image.Rectangle, c color.Color) {
		draw.Draw(img, ret, image.NewUniform(c), image.Point{}, draw.Src)
	}
	cx, cy := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
	cima := image.Rect(cx, r.Min.Y, cx+1, cy+1)
	baixo := image.Rect(cx, cy, cx+1, r.Max.Y)
	esquerda := image.Rect(r.Min.X, cy, cx+1, cy+1)
	direita := image.Rect(cx, cy, r.Max.X, cy+1)
	partes := map[string][]image.Rectangle{
		"│": {cima, baixo}, "─": {esquerda, direita}, "┼": {cima, baixo, esquerda, direita},
		"┬": {baixo, esquerda, direita}, "┴": {cima, esquerda, direita},
		"├": {cima, baixo, direita}, "┤": {cima, baixo, esquerda},
		"┌": {baixo, direita}, "┐": {baixo, esquerda}, "└": {cima, direita}, "┘": {cima, esquerda},
	}
	if p, ok := partes[s]; ok {
		for _, ret := range p {
			preencher(ret, frente)
		}
		return true
	}
	densidade := map[string]float64{"█": 1, "▓": 0.75, "▒": 0.5, "░": 0.25}
	if d, ok := densidade[s]; ok {
		preencher(r, misturar(frente, tras, d))
		return true
	}
	return false
}

// misturar dá a cor a, na proporção p, sobre a cor b.
func misturar(a, b color.Color, p float64) color.Color {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	mix := func(x, y uint32) uint8 { return uint8((p*float64(x) + (1-p)*float64(y)) / 257) }
	return color.RGBA{mix(ar, br), mix(ag, bg), mix(ab, bb), 255}
}
