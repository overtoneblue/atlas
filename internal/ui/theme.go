package ui

import "github.com/charmbracelet/lipgloss"

// Atlas palette — carried over from the design mock in the
// rich-desktop-hermes-app investigation (2026-09-13).
var (
	colDim   = lipgloss.Color("242")
	colFaint = lipgloss.Color("240")
	colGold  = lipgloss.Color("215")
	colChan  = lipgloss.Color("110")
	colPost  = lipgloss.Color("245")
	colGreen = lipgloss.Color("114")
	colMauve = lipgloss.Color("176")
	colBlu   = lipgloss.Color("117")
	colYel   = lipgloss.Color("222")
	colRed   = lipgloss.Color("203")
	colMag   = lipgloss.Color("170")
	colFgHi  = lipgloss.Color("255")
)

var (
	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(colFgHi)
	styleDim    = lipgloss.NewStyle().Foreground(colDim)
	styleFaint  = lipgloss.NewStyle().Foreground(colFaint)
	styleGold   = lipgloss.NewStyle().Bold(true).Foreground(colGold)
	styleChan   = lipgloss.NewStyle().Foreground(colChan)
	stylePost   = lipgloss.NewStyle().Foreground(colPost)
	styleGreen  = lipgloss.NewStyle().Foreground(colGreen)
	styleMauve  = lipgloss.NewStyle().Foreground(colMauve)
	styleBlu    = lipgloss.NewStyle().Foreground(colBlu)
	styleYel    = lipgloss.NewStyle().Foreground(colYel)
	styleRed    = lipgloss.NewStyle().Foreground(colRed)
	styleMag    = lipgloss.NewStyle().Foreground(colMag)
	styleSep    = lipgloss.NewStyle().Foreground(colDim)
	styleStatus = lipgloss.NewStyle().Background(lipgloss.Color("235")).Foreground(lipgloss.Color("250"))
	styleInsert = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("60")).Foreground(colFgHi)
)
