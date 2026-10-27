package main

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

type estilos struct {
	marca, abaAtiva, abaInativa, abaEstado, abaEstadoAtiva, status  lipgloss.Style
	simulacao                                                       lipgloss.Style
	titulo, fraco, negrito, celula, rotulo, cabecalho, separador    lipgloss.Style
	aviso, positivo, negativo, eleito, segundoTurno, naoEleito, fim lipgloss.Style
	ganhando, perdendo                                              lipgloss.Style
	grupo                                                           [3]lipgloss.Style
	corCand                                                         [2]color.Color
	corOutros, corTotal, corSelecao, corVazio                       color.Color
	fundoPadrao, textoPadrao                                        color.Color // da foto, se o terminal não informar
}

func novosEstilos(escuro bool) estilos {
	ld := lipgloss.LightDark(escuro)
	cor := func(claro, escuro string) color.Color { return ld(lipgloss.Color(claro), lipgloss.Color(escuro)) }
	texto := cor("#1F2937", "#E5E7EB")
	apagado := cor("#6B7280", "#9CA3AF")
	destaque := cor("#4338CA", "#A5B4FC")
	estilo := lipgloss.NewStyle

	e := estilos{
		// Cores neutras para os dois candidatos do 2º turno, na ordem da urna.
		corCand:    [2]color.Color{cor("#B45309", "#FBBF24"), cor("#0E7490", "#22D3EE")},
		corOutros:  apagado,
		corTotal:   cor("#E5E7EB", "#1F2937"),
		corSelecao: cor("#C7D2FE", "#312E81"),
		corVazio:   cor("#D1D5DB", "#374151"),
		// O fundo e o texto padrão de terminais claros e escuros, como os do Alacritty.
		fundoPadrao: cor("#FFFFFF", "#181818"),
		textoPadrao: cor("#1F2937", "#D8D8D8"),
	}
	e.marca = estilo().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(cor("#4338CA", "#4F46E5"))
	e.abaAtiva = estilo().Bold(true).Foreground(destaque).Underline(true)
	e.abaInativa = estilo().Foreground(apagado)
	e.abaEstado = estilo().Foreground(apagado).Padding(0, 1)
	e.abaEstadoAtiva = e.marca.Padding(0, 1)
	e.status = estilo().Foreground(apagado)
	e.simulacao = estilo().Bold(true).Foreground(lipgloss.Color("#111827")).Background(lipgloss.Color("#FACC15"))
	e.titulo = estilo().Bold(true).Foreground(texto)
	e.fraco = estilo().Foreground(apagado)
	e.negrito = estilo().Bold(true).Foreground(texto)
	e.celula = estilo().Foreground(texto)
	e.rotulo = estilo().Bold(true).Foreground(texto)
	e.cabecalho = estilo().Bold(true).Foreground(destaque)
	e.separador = estilo().Foreground(cor("#D1D5DB", "#4B5563"))
	e.aviso = estilo().Foreground(cor("#B91C1C", "#F87171"))
	e.positivo = estilo().Foreground(cor("#15803D", "#4ADE80"))
	e.negativo = estilo().Foreground(cor("#B91C1C", "#F87171"))
	e.eleito = estilo().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#15803D")).Padding(0, 1)
	e.segundoTurno = estilo().Bold(true).Foreground(lipgloss.Color("#111827")).Background(lipgloss.Color("#FBBF24")).Padding(0, 1)
	e.naoEleito = estilo().Foreground(apagado)
	e.fim = estilo().Bold(true).Foreground(cor("#15803D", "#4ADE80"))
	// Fundo do percentual no 2º turno: verde para quem lidera na linha, vermelho para quem está atrás.
	e.ganhando = estilo().Foreground(cor("#14532D", "#F0FDF4")).Background(cor("#BBF7D0", "#166534"))
	e.perdendo = estilo().Foreground(cor("#7F1D1D", "#FEF2F2")).Background(cor("#FECACA", "#991B1B"))
	fundos := [3]color.Color{cor("#DBEAFE", "#1E3A8A"), cor("#EDE9FE", "#4C1D95"), cor("#D1FAE5", "#065F46")}
	for i := range e.grupo {
		e.grupo[i] = estilo().Bold(true).Foreground(texto).Background(fundos[i])
	}
	return e
}
