package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// The interface's icons are Lucide's, reached through Onevra's named
// accessors rather than local SVG copies: one stroke language, one source,
// and size and color stay the caller's.
var (
	icBack       = lucide.ArrowLeft()
	icForward    = lucide.ArrowRight()
	icPanel      = lucide.PanelLeft()
	icPanelRight = lucide.PanelRight()
	icHome       = lucide.House()
	icStack      = lucide.Layers()
	icHistory    = lucide.Clock()
	icGitBranch  = lucide.GitBranch()
	icGrid       = lucide.LayoutGrid()
	icClose      = lucide.X()
	icGlobe      = lucide.Globe()
	icGear       = lucide.Settings()
	icServer     = lucide.Server()
	icCpu        = lucide.Cpu()
	icKey        = lucide.Key()
	icBell       = lucide.Bell()
	icSearch     = lucide.Search()
	icPen        = lucide.SquarePen()
	icFolder     = lucide.Folder()
	icFolderLink = lucide.FolderSymlink()
	icFileDoc    = lucide.FileText()
	icTerminal   = lucide.Terminal()
	icBook       = lucide.BookOpen()
	icPlug       = lucide.Plug()
	icChevRight  = lucide.ChevronRight()
	icChevUp     = lucide.ChevronUp()
	icChevDown   = lucide.ChevronDown()
	icEllipsis   = lucide.Ellipsis()
	icTrash      = lucide.Trash()
	icCheck      = lucide.Check()
	icPlus       = lucide.Plus()
	icShield     = lucide.ShieldCheck()
	icLoad       = lucide.RefreshCw()
	icSparkle    = lucide.Sparkles()
	icChat       = lucide.MessageSquare()
	icCopy       = lucide.Copy()
	icQuote      = lucide.TextQuote()
	icExpand     = lucide.Maximize2()
	icCollapse   = lucide.Minimize2()
	icActivity   = lucide.Activity()
	icFilter     = lucide.Funnel()
	icExternal   = lucide.ExternalLink()
	icCommand    = lucide.Command()
	icAppearance = lucide.Palette()
	icSun        = lucide.Sun()
	icMoon       = lucide.Moon()
	icDiffRows   = lucide.Rows3()
	icDiffSplit  = lucide.Columns2()
)

// iconRef adapts one of the shared icons to Onevra APIs that take an icon
// getter, such as chat chips.
func iconRef(icon *ui.SVG) func() *ui.SVG { return func() *ui.SVG { return icon } }
