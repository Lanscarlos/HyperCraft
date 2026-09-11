package launchscript

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// arg is one expanded argument, carrying what could not be resolved about it.
type arg struct {
	text       string
	unresolved []string
	sub        bool
	passthru   bool
}

// candidate is a command line that launches a JVM, after its wrappers are off.
type candidate struct {
	words    []arg
	wrappers []string
	redirect bool
	line     logicalLine
}

type parser struct {
	dialect    Dialect
	vars       map[string]string
	blocks     []string
	candidates []candidate
	others     []logicalLine
	refusals   []Refusal
}

// Parse reads a start script. A non-empty refusal list means the Result is
// meaningless: there is no partial success here.
func Parse(text string, dialect Dialect) (Result, []Refusal) {
	p := &parser{dialect: dialect, vars: map[string]string{}}
	for _, line := range logicalLines(text, dialect) {
		p.readLine(line)
		if len(p.refusals) > 0 {
			return Result{}, p.refusals
		}
	}
	return p.finish()
}

// ParseArgv takes apart a command line that is already split into arguments,
// which is what an instance migrating off the old script mode has instead of a
// script.
func ParseArgv(argv []string) (Result, []Refusal) {
	words := make([]arg, 0, len(argv))
	for _, a := range argv {
		words = append(words, arg{text: a})
	}
	words, wrappers := stripWrappers(words)
	if len(words) == 0 || !isJava(words[0].text) {
		return Result{}, []Refusal{{
			Code:   "not-java",
			Reason: "这条启动命令不是在启动 Java，面板只能接管 Java 服务端的启动参数。",
			Text:   strings.Join(argv, " "),
		}}
	}
	return analyse(words, wrappers, logicalLine{text: strings.Join(argv, " ")})
}

func (p *parser) readLine(line logicalLine) {
	for _, cmd := range splitCommands(tokenize(line.text, p.dialect)) {
		p.readCommand(cmd, line)
		if len(p.refusals) > 0 {
			return
		}
	}
}

// command is one simple command: its words, plus what the shell was going to
// do with its output.
type command struct {
	words      []token
	redirect   bool
	background bool
}

// splitCommands cuts a line at ; && || and pulls the redirections and the
// trailing & out of each piece.
func splitCommands(tokens []token) []command {
	var out []command
	cur := command{}
	flush := func() {
		if len(cur.words) > 0 || cur.redirect {
			out = append(out, cur)
		}
		cur = command{}
	}

	for i, tk := range tokens {
		if !tk.isOp() {
			if !cur.redirect {
				cur.words = append(cur.words, tk)
			}
			continue
		}
		switch tk.op {
		case ";", "&&", "||":
			flush()
		case "|", ">", ">>", "<":
			cur.redirect = true
		case "&":
			// A lone & at the very end backgrounds the command; anywhere else
			// it is part of a redirection like 2>&1, which the redirect flag
			// has already accounted for.
			if i == len(tokens)-1 {
				cur.background = true
			}
		}
	}
	flush()
	return out
}

var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// blockOpeners map a shell keyword to what it means for anything inside it.
var blockOpeners = map[string]string{
	"while": "loop", "until": "loop", "for": "loop",
	"if": "if", "case": "case",
}

var blockClosers = map[string]bool{"done": true, "fi": true, "esac": true}

var blockNoise = map[string]bool{"do": true, "then": true, "else": true, "elif": true, "in": true}

// harmless are the commands a start script runs around the launch. Seeing one
// of these is not evidence that the script launches something else.
var harmless = map[string]bool{
	"echo": true, "cd": true, "mkdir": true, "sleep": true, "read": true,
	"pause": true, "exit": true, "set": true, "chmod": true, "true": true,
	"false": true, "test": true, "[": true, "printf": true, "cls": true,
	"clear": true, "title": true, "color": true, "timeout": true, "wait": true,
	"export": true, "unset": true, "touch": true, "date": true, "trap": true,
	"shift": true, "umask": true, "ulimit": true, "tput": true, "rm": true,
}

func (p *parser) readCommand(cmd command, line logicalLine) {
	words := cmd.words
	if len(words) == 0 {
		return
	}

	first := words[0].raw
	if p.dialect == Batch {
		first = strings.ToLower(first)
	}

	if blockClosers[first] {
		if len(p.blocks) > 0 {
			p.blocks = p.blocks[:len(p.blocks)-1]
		}
		return
	}
	if kind, ok := blockOpeners[first]; ok && p.dialect == Shell {
		p.blocks = append(p.blocks, kind)
		return
	}
	if blockNoise[first] {
		return
	}
	if p.dialect == Shell && (first == "source" || first == ".") {
		p.refuse("external-source", "这个脚本从别的文件里读配置，那些值面板看不到，拆出来的命令行就可能不是你实际在跑的。", line)
		return
	}

	words = p.takeAssignments(words)
	if len(words) == 0 {
		return
	}
	if strings.ToLower(words[0].raw) == "cd" {
		p.readCd(words, line)
		return
	}

	expanded := make([]arg, 0, len(words))
	for _, tk := range words {
		ex := expand(tk, p.vars, p.dialect)
		expanded = append(expanded, arg{text: ex.text, unresolved: ex.unresolved, sub: ex.sub, passthru: ex.passthru})
	}

	expanded, wrappers := stripWrappers(expanded)
	if len(expanded) == 0 {
		return
	}
	if cmd.background {
		wrappers = append(wrappers, "&")
	}

	if !isJava(expanded[0].text) {
		if !harmless[strings.ToLower(expanded[0].text)] {
			p.others = append(p.others, line)
		}
		return
	}
	if len(p.blocks) > 0 {
		kind := p.blocks[len(p.blocks)-1]
		if kind == "loop" {
			p.refuse("wrapped-in-loop", "服务端被写在循环里，这是脚本自己在做自动重启。面板有自己的自动重启，两套叠在一起会把同一个世界开两份。", line)
			return
		}
		p.refuse("conditional", "服务端写在条件分支里，面板无法确定哪一条才是你实际在跑的那条。", line)
		return
	}
	p.candidates = append(p.candidates, candidate{words: expanded, wrappers: wrappers, redirect: cmd.redirect, line: line})
}

// takeAssignments records the NAME=value words in front of a command and
// returns what is left. They are a prefix, not a command: "LD_LIBRARY_PATH=.
// ./bedrock_server" is one launch with one variable set for it.
func (p *parser) takeAssignments(words []token) []token {
	if p.dialect == Batch {
		if strings.ToLower(words[0].raw) == "set" && len(words) > 1 && assignment.MatchString(words[1].raw) {
			p.record(words[1].raw)
			return nil
		}
		return words
	}
	for len(words) > 0 {
		raw := words[0].raw
		if raw == "export" && len(words) > 1 && assignment.MatchString(words[1].raw) {
			p.record(words[1].raw)
			words = words[2:]
			continue
		}
		if !assignment.MatchString(raw) {
			break
		}
		p.record(raw)
		words = words[1:]
	}
	return words
}

// record stores a variable's value unexpanded, so that a later reference
// resolves it against everything known by then rather than by now.
func (p *parser) record(raw string) {
	name, value, _ := strings.Cut(raw, "=")
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		value = value[1 : len(value)-1]
	}
	p.vars[name] = value
}

// readCd allows the one cd a start script is entitled to: into its own
// directory, which is where the panel starts the process anyway.
func (p *parser) readCd(words []token, line logicalLine) {
	for _, tk := range words[1:] {
		raw := tk.raw
		if strings.Contains(raw, "$0") || strings.Contains(raw, "${0") || raw == "." || raw == "./" {
			return
		}
	}
	if len(words) == 1 {
		return
	}
	p.refuse("cd-elsewhere", "启动前切到了别的目录，面板只会在实例目录里启动进程，拆出来的相对路径会指向别处。", line)
}

func (p *parser) refuse(code, reason string, line logicalLine) {
	p.refusals = append(p.refusals, Refusal{Code: code, Reason: reason, Line: line.num, Text: line.text})
}

func (p *parser) finish() (Result, []Refusal) {
	if len(p.candidates) == 0 {
		if len(p.others) > 0 {
			last := p.others[len(p.others)-1]
			return Result{}, []Refusal{{
				Code:   "not-java",
				Reason: "这个脚本启动的不是 Java 程序，面板只能接管 Java 服务端的启动参数。",
				Line:   last.num,
				Text:   last.text,
			}}
		}
		return Result{}, []Refusal{{
			Code:   "no-java",
			Reason: "这个脚本里找不到启动服务端的那一行。",
		}}
	}

	first := p.candidates[0]
	for _, c := range p.candidates[1:] {
		if key(c.words) == key(first.words) {
			continue
		}
		return Result{}, []Refusal{{
			Code:   "multiple-java",
			Reason: "这个脚本里有不止一条启动命令，面板无法确定该听哪一条。",
			Line:   c.line.num,
			Text:   c.line.text,
		}}
	}
	if first.redirect {
		return Result{}, []Refusal{{
			Code:   "redirection",
			Reason: "启动命令把输出重定向或接进了管道，面板要自己接管控制台输出，照搬这条命令行行为会和原来不一样。",
			Line:   first.line.num,
			Text:   first.line.text,
		}}
	}
	return analyse(first.words, first.wrappers, first.line)
}

func key(words []arg) string {
	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = w.text
	}
	return strings.Join(parts, "\x00")
}

// wrapperNames are the ways a script hands the JVM to something else. The
// panel runs the JVM itself, so these come off — and get reported, because
// "your server will no longer be inside screen" is news.
var wrapperNames = map[string]bool{"exec": true, "nohup": true, "setsid": true}

// hostWrappers put the JVM inside a terminal multiplexer. The command line is
// somewhere in their arguments rather than after a fixed number of them.
var hostWrappers = map[string]bool{"screen": true, "tmux": true}

func stripWrappers(words []arg) ([]arg, []string) {
	var wrappers []string
	for len(words) > 0 {
		name := strings.ToLower(path.Base(strings.ReplaceAll(words[0].text, "\\", "/")))
		switch {
		case wrapperNames[name]:
			wrappers = append(wrappers, name)
			words = words[1:]
		case hostWrappers[name]:
			at := -1
			for i := 1; i < len(words); i++ {
				if isJava(words[i].text) {
					at = i
					break
				}
			}
			if at < 0 {
				return words, wrappers
			}
			wrappers = append(wrappers, name)
			words = words[at:]
		default:
			return words, wrappers
		}
	}
	return words, wrappers
}

func isJava(text string) bool {
	base := strings.ToLower(path.Base(strings.ReplaceAll(text, "\\", "/")))
	base = strings.TrimSuffix(base, ".exe")
	return base == "java" || base == "javaw"
}

// analyse turns one java command line into launch settings.
func analyse(words []arg, wrappers []string, line logicalLine) (Result, []Refusal) {
	out := Result{Wrappers: wrappers}
	refuse := func(code, reason string) (Result, []Refusal) {
		return Result{}, []Refusal{{Code: code, Reason: reason, Line: line.num, Text: line.text}}
	}

	java := words[0]
	if strings.HasSuffix(strings.TrimSuffix(strings.ToLower(java.text), ".exe"), "javaw") {
		out.Javaw = true
		out.Java = strings.Replace(java.text, "javaw", "java", 1)
	} else {
		out.Java = java.text
	}
	if len(java.unresolved) > 0 {
		out.JavaVar = java.unresolved[0]
		out.Java = ""
	}

	args := words[1:]
	afterJar := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		// "$@" at the end is the script's own way of letting its caller add
		// --nogui. The panel supplies those arguments itself, so the idiom is
		// nothing to carry over — anywhere else it stands for arguments nobody
		// here can know.
		if a.passthru && i == len(args)-1 {
			continue
		}
		if code, reason := distrust(a); code != "" {
			return refuse(code, reason)
		}

		text := a.text
		switch {
		case afterJar:
			out.ServerArgs = append(out.ServerArgs, text)
		case text == "-jar":
			if i+1 >= len(args) {
				return refuse("no-target", "启动命令里的 -jar 后面没有跟核心文件。")
			}
			// The jar's own name goes through the same check: consuming it
			// here is what would otherwise let an unresolved $JARFILE past.
			if code, reason := distrust(args[i+1]); code != "" {
				return refuse(code, reason)
			}
			out.Jar = args[i+1].text
			afterJar = true
			i++
		case strings.HasPrefix(text, "@"):
			out.ArgFiles = append(out.ArgFiles, strings.TrimPrefix(text, "@"))
		case text == "-cp" || text == "-classpath" || text == "--class-path":
			return refuse("classpath-launch", "这个脚本用 -cp 加主类启动，面板拼不出这种命令行。")
		case strings.HasPrefix(text, "-Xmx"):
			out.MaxMemoryMB = megabytes(strings.TrimPrefix(text, "-Xmx"))
		case strings.HasPrefix(text, "-Xms"):
			out.MinMemoryMB = megabytes(strings.TrimPrefix(text, "-Xms"))
		case len(out.ArgFiles) > 0:
			// In argfile mode the JVM flags live in the argfile. Anything the
			// script still writes on the command line after them is for the
			// server — which is how "@unix_args.txt --nogui" keeps --nogui out
			// of the JVM arguments, where it would stop the server from
			// starting at all.
			out.ServerArgs = append(out.ServerArgs, text)
		case strings.HasPrefix(text, "-"):
			out.JVMArgs = append(out.JVMArgs, text)
		default:
			return refuse("classpath-launch", "这个脚本直接指定主类启动，面板拼不出这种命令行。")
		}
	}

	if out.Jar == "" && len(out.ArgFiles) == 0 {
		return refuse("no-target", "启动命令里既没有 -jar，也没有 @参数文件，面板不知道该启动什么。")
	}
	return out, nil
}

// distrust reports why one argument cannot be carried over, empty when it can.
func distrust(a arg) (code, reason string) {
	switch {
	case a.passthru:
		return "passthrough-args", "启动命令里有 \"$@\"，它展开成什么取决于是谁在调这个脚本，面板无法确定。"
	case len(a.unresolved) > 0:
		return "unknown-var", "启动命令里的 $" + a.unresolved[0] + " 在脚本里没有定义，面板不会去猜它的值。"
	case a.sub:
		return "command-substitution", "启动命令里嵌了一条要执行才知道结果的命令，而面板不会执行你的脚本。"
	}
	return "", ""
}

// megabytes reads a JVM size argument. No suffix means bytes, which is what
// -Xmx1073741824 relies on.
func megabytes(size string) int {
	if size == "" {
		return 0
	}
	unit := size[len(size)-1]
	digits := size
	scale := int64(1)
	switch unit {
	case 'k', 'K':
		scale, digits = 1<<10, size[:len(size)-1]
	case 'm', 'M':
		scale, digits = 1<<20, size[:len(size)-1]
	case 'g', 'G':
		scale, digits = 1<<30, size[:len(size)-1]
	case 't', 'T':
		scale, digits = 1<<40, size[:len(size)-1]
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return int(n * scale / (1 << 20))
}
