package mission

import "github.com/charmbracelet/lipgloss"

type theme struct {
	name    string
	primary lipgloss.Color
	dim     lipgloss.Color
	panel   lipgloss.Color
	bg      lipgloss.Color
	accent  lipgloss.Color
	warn    lipgloss.Color
	err     lipgloss.Color
	text    lipgloss.Color
}

var themes = []theme{
	{"green", lipgloss.Color("46"), lipgloss.Color("28"), lipgloss.Color("22"), lipgloss.Color("0"), lipgloss.Color("118"), lipgloss.Color("220"), lipgloss.Color("196"), lipgloss.Color("230")},
	{"cyan", lipgloss.Color("51"), lipgloss.Color("37"), lipgloss.Color("30"), lipgloss.Color("0"), lipgloss.Color("87"), lipgloss.Color("229"), lipgloss.Color("203"), lipgloss.Color("231")},
	{"amber", lipgloss.Color("220"), lipgloss.Color("136"), lipgloss.Color("94"), lipgloss.Color("0"), lipgloss.Color("214"), lipgloss.Color("230"), lipgloss.Color("196"), lipgloss.Color("230")},
	{"blue", lipgloss.Color("39"), lipgloss.Color("24"), lipgloss.Color("17"), lipgloss.Color("0"), lipgloss.Color("75"), lipgloss.Color("220"), lipgloss.Color("196"), lipgloss.Color("231")},
	{"purple", lipgloss.Color("141"), lipgloss.Color("60"), lipgloss.Color("54"), lipgloss.Color("0"), lipgloss.Color("183"), lipgloss.Color("220"), lipgloss.Color("196"), lipgloss.Color("231")},
	{"red", lipgloss.Color("196"), lipgloss.Color("88"), lipgloss.Color("52"), lipgloss.Color("0"), lipgloss.Color("203"), lipgloss.Color("220"), lipgloss.Color("196"), lipgloss.Color("231")},
	{"white", lipgloss.Color("255"), lipgloss.Color("245"), lipgloss.Color("238"), lipgloss.Color("0"), lipgloss.Color("250"), lipgloss.Color("220"), lipgloss.Color("196"), lipgloss.Color("255")},
}
