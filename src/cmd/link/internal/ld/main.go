// Inferno utils/6l/obj.c
// https://bitbucket.org/inferno-os/inferno-os/src/master/utils/6l/obj.c
//
//	Copyright © 1994-1999 Lucent Technologies Inc.  All rights reserved.
//	Portions Copyright © 1995-1997 C H Forsyth (forsyth@terzarima.net)
//	Portions Copyright © 1997-1999 Vita Nuova Limited
//	Portions Copyright © 2000-2007 Vita Nuova Holdings Limited (www.vitanuova.com)
//	Portions Copyright © 2004,2006 Bruce Ellis
//	Portions Copyright © 2005-2007 C H Forsyth (forsyth@terzarima.net)
//	Revisions Copyright © 2000-2007 Lucent Technologies Inc. and others
//	Portions Copyright © 2009 The Go Authors. All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.  IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package ld

import (
	"bufio"
	"cmd/internal/goobj"
	"cmd/internal/objabi"
	"cmd/internal/sys"
	"cmd/link/internal/benchmark"
	"flag"
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
)

var (
	pkglistfornote []byte
	windowsgui     bool // writes a "GUI binary" instead of a "console binary"
	ownTmpDir      bool // set to true if tmp dir created by linker (e.g. no -tmpdir)
)

func init() {
	flag.Var(&rpath, "r", "set the ELF dynamic linker search `path` to dir1:dir2:...")
}

// Flags used by the linker. The exported flags are used by the architecture-specific packages.
var (
	flagBuildid = flag.String("buildid", "", "record `id` as Go toolchain build id")

	flagOutfile    = flag.String("o", "", "write output to `file`")
	flagPluginPath = flag.String("pluginpath", "", "full path name for plugin")

	flagInstallSuffix = flag.String("installsuffix", "", "set package directory `suffix`")
	flagDumpDep       = flag.Bool("dumpdep", false, "dump symbol dependency graph")
	flagRace          = flag.Bool("race", false, "enable race detector")
	flagMsan          = flag.Bool("msan", false, "enable MSan interface")
	flagAslr          = flag.Bool("aslr", true, "enable ASLR for buildmode=c-shared on windows")

	flagFieldTrack = flag.String("k", "", "set field tracking `symbol`")
	flagLibGCC     = flag.String("libgcc", "", "compiler support lib for internal linking; use \"none\" to disable")
	flagTmpdir     = flag.String("tmpdir", "", "use `directory` for temporary files")

	flagExtld      = flag.String("extld", "", "use `linker` when linking in external mode")
	flagExtldflags = flag.String("extldflags", "", "pass `flags` to external linker")
	flagExtar      = flag.String("extar", "", "archive program for buildmode=c-archive")

	flagA             = flag.Bool("a", false, "no-op (deprecated)")
	FlagC             = flag.Bool("c", false, "dump call graph")
	FlagD             = flag.Bool("d", false, "disable dynamic executable")
	flagF             = flag.Bool("f", false, "ignore version mismatch")
	flagG             = flag.Bool("g", false, "disable go package data checks")
	flagH             = flag.Bool("h", false, "halt on error")
	flagN             = flag.Bool("n", false, "dump symbol table")
	FlagS             = flag.Bool("s", false, "disable symbol table")
	FlagW             = flag.Bool("w", false, "disable DWARF generation")
	flag8             bool // use 64-bit addresses in symbol table
	flagInterpreter   = flag.String("I", "", "use `linker` as ELF dynamic linker")
	FlagDebugTramp    = flag.Int("debugtramp", 0, "debug trampolines")
	FlagDebugTextSize = flag.Int("debugppc64textsize", 0, "debug PPC64 text section max")
	FlagStrictDups    = flag.Int("strictdups", 0, "sanity check duplicate symbol contents during object file reading (1=warn 2=err).")
	FlagRound         = flag.Int("R", -1, "set address rounding `quantum`")
	FlagTextAddr      = flag.Int64("T", -1, "set text segment `address`")
	flagEntrySymbol   = flag.String("E", "", "set `entry` symbol name")
	cpuprofile        = flag.String("cpuprofile", "", "write cpu profile to `file`")
	memprofile        = flag.String("memprofile", "", "write memory profile to `file`")
	memprofilerate    = flag.Int64("memprofilerate", 0, "set runtime.MemProfileRate to `rate`")
	benchmarkFlag     = flag.String("benchmark", "", "set to 'mem' or 'cpu' to enable phase benchmarking")
	benchmarkFileFlag = flag.String("benchmarkprofile", "", "emit phase profiles to `base`_phase.{cpu,mem}prof")
)

// Main is the main entry point for the linker code.
func Main(arch *sys.Arch, theArch Arch) {
	thearch = theArch
	ctxt := linknew(arch)
	ctxt.Bso = bufio.NewWriter(os.Stdout)

	// For testing behavior of go command when tools crash silently.
	// Undocumented, not in standard flag parser to avoid
	// exposing in usage message.
	for _, arg := range os.Args {
		if arg == "-crash_for_testing" {
			os.Exit(2)
		}
	}

	final := gorootFinal()
	addstrdata1(ctxt, "runtime.defaultGOROOT="+final)
	addstrdata1(ctxt, "cmd/internal/objabi.defaultGOROOT="+final)

	addstrdata1(ctxt, "runtime/internal/sys.GOEXPERIMENT="+objabi.GOEXPERIMENT)

	// TODO(matloob): define these above and then check flag values here
	if ctxt.Arch.Family == sys.AMD64 && objabi.GOOS == "plan9" {
		flag.BoolVar(&flag8, "8", false, "use 64-bit addresses in symbol table")
	}
	flagHeadType := flag.String("H", "", "set header `type`")
	flag.BoolVar(&ctxt.linkShared, "linkshared", false, "link against installed Go shared libraries")
	flag.Var(&ctxt.LinkMode, "linkmode", "set link `mode`")
	flag.Var(&ctxt.BuildMode, "buildmode", "set build `mode`")
	flag.BoolVar(&ctxt.compressDWARF, "compressdwarf", true, "compress DWARF if possible")
	objabi.Flagfn1("B", "add an ELF NT_GNU_BUILD_ID `note` when using ELF", addbuildinfo)
	objabi.Flagfn1("L", "add specified `directory` to library path", func(a string) { Lflag(ctxt, a) })
	objabi.AddVersionFlag() // -V
	objabi.Flagfn1("X", "add string value `definition` of the form importpath.name=value", func(s string) { addstrdata1(ctxt, s) })
	objabi.Flagcount("v", "print link trace", &ctxt.Debugvlog)
	objabi.Flagfn1("importcfg", "read import configuration from `file`", ctxt.readImportCfg)

	objabi.Flagparse(usage)

	if ctxt.Debugvlog > 0 {
		// dump symbol info on crash
		defer func() { ctxt.loader.Dump() }()
	}

	switch *flagHeadType {
	case "":
	case "windowsgui":
		ctxt.HeadType = objabi.Hwindows
		windowsgui = true
	default:
		if err := ctxt.HeadType.Set(*flagHeadType); err != nil {
			Errorf(nil, "%v", err)
			usage()
		}
	}
	if ctxt.HeadType == objabi.Hunknown {
		ctxt.HeadType.Set(objabi.GOOS)
	}

	if !*flagAslr && ctxt.BuildMode != BuildModeCShared {
		Errorf(nil, "-aslr=false is only allowed for -buildmode=c-shared")
		usage()
	}

	checkStrictDups = *FlagStrictDups

	startProfile()
	if ctxt.BuildMode == BuildModeUnset {
		ctxt.BuildMode.Set("exe")
	}

	if ctxt.BuildMode != BuildModeShared && flag.NArg() != 1 {
		usage()
	}

	if *flagOutfile == "" {
		*flagOutfile = "a.out"
		if ctxt.HeadType == objabi.Hwindows {
			*flagOutfile += ".exe"
		}
	}

	interpreter = *flagInterpreter

	if *flagBuildid == "" && ctxt.Target.IsOpenbsd() {
		// TODO(jsing): Remove once direct syscalls are no longer in use.
		// OpenBSD 6.7 onwards will not permit direct syscalls from a
		// dynamically linked binary unless it identifies the binary
		// contains a .note.go.buildid ELF note. See issue #36435.
		*flagBuildid = "go-openbsd"
	}

	// enable benchmarking
	var bench *benchmark.Metrics
	if len(*benchmarkFlag) != 0 {
		if *benchmarkFlag == "mem" {
			bench = benchmark.New(benchmark.GC, *benchmarkFileFlag)
		} else if *benchmarkFlag == "cpu" {
			bench = benchmark.New(benchmark.NoGC, *benchmarkFileFlag)
		} else {
			Errorf(nil, "unknown benchmark flag: %q", *benchmarkFlag)
			usage()
		}
	}

	bench.Start("libinit")
	libinit(ctxt) // creates outfile
	bench.Start("computeTLSOffset")
	ctxt.computeTLSOffset()
	bench.Start("Archinit")
	thearch.Archinit(ctxt)

	if ctxt.linkShared && !ctxt.IsELF {
		Exitf("-linkshared can only be used on elf systems")
	}

	if ctxt.Debugvlog != 0 {
		ctxt.Logf("HEADER = -H%d -T0x%x -R0x%x\n", ctxt.HeadType, uint64(*FlagTextAddr), uint32(*FlagRound))
	}

	zerofp := goobj.FingerprintType{}
	switch ctxt.BuildMode {
	case BuildModeShared:
		for i := 0; i < flag.NArg(); i++ {
			arg := flag.Arg(i)
			parts := strings.SplitN(arg, "=", 2)
			var pkgpath, file string
			if len(parts) == 1 {
				pkgpath, file = "main", arg
			} else {
				pkgpath, file = parts[0], parts[1]
			}
			pkglistfornote = append(pkglistfornote, pkgpath...)
			pkglistfornote = append(pkglistfornote, '\n')
			addlibpath(ctxt, "command line", "command line", file, pkgpath, "", zerofp)
		}
	case BuildModePlugin:
		addlibpath(ctxt, "command line", "command line", flag.Arg(0), *flagPluginPath, "", zerofp)
	default:
		addlibpath(ctxt, "command line", "command line", flag.Arg(0), "main", "", zerofp)
	}
	bench.Start("loadlib")
	ctxt.loadlib()

	bench.Start("deadcode")
	deadcode(ctxt)

	bench.Start("linksetup")
	ctxt.linksetup()

	bench.Start("dostrdata")
	ctxt.dostrdata()
	if objabi.Fieldtrack_enabled != 0 {
		bench.Start("fieldtrack")
		fieldtrack(ctxt.Arch, ctxt.loader)
	}

	bench.Start("dwarfGenerateDebugInfo")
	dwarfGenerateDebugInfo(ctxt)

	bench.Start("callgraph")
	ctxt.callgraph()

	bench.Start("dostkcheck")
	ctxt.dostkcheck()

	bench.Start("mangleTypeSym")
	ctxt.mangleTypeSym()

	if ctxt.IsELF {
		bench.Start("doelf")
		ctxt.doelf()
	}
	if ctxt.IsDarwin() {
		bench.Start("domacho")
		ctxt.domacho()
	}
	if ctxt.IsWindows() {
		bench.Start("dope")
		ctxt.dope()
		bench.Start("windynrelocsyms")
		ctxt.windynrelocsyms()
	}
	if ctxt.IsAIX() {
		bench.Start("doxcoff")
		ctxt.doxcoff()
	}

	bench.Start("textbuildid")
	ctxt.textbuildid()
	bench.Start("addexport")
	setupdynexp(ctxt)
	ctxt.setArchSyms()
	ctxt.addexport()
	bench.Start("Gentext")
	thearch.Gentext(ctxt, ctxt.loader) // trampolines, call stubs, etc.

	bench.Start("textaddress")
	ctxt.textaddress()
	bench.Start("typelink")
	ctxt.typelink()
	bench.Start("buildinfo")
	ctxt.buildinfo()
	bench.Start("pclntab")
	containers := ctxt.findContainerSyms()
	pclnState := ctxt.pclntab(containers)
	bench.Start("findfunctab")
	ctxt.findfunctab(pclnState, containers)
	bench.Start("dwarfGenerateDebugSyms")
	dwarfGenerateDebugSyms(ctxt)
	bench.Start("symtab")
	symGroupType := ctxt.symtab(pclnState)
	bench.Start("dodata")
	ctxt.dodata(symGroupType)
	bench.Start("address")
	order := ctxt.address()
	bench.Start("dwarfcompress")
	dwarfcompress(ctxt)
	bench.Start("layout")
	filesize := ctxt.layout(order)

	// Write out the output file.
	// It is split into two parts (Asmb and Asmb2). The first
	// part writes most of the content (sections and segments),
	// for which we have computed the size and offset, in a
	// mmap'd region. The second part writes more content, for
	// which we don't know the size.
	if ctxt.Arch.Family != sys.Wasm {
		// Don't mmap if we're building for Wasm. Wasm file
		// layout is very different so filesize is meaningless.
		if err := ctxt.Out.Mmap(filesize); err != nil {
			panic(err)
		}
	}
	// asmb will redirect symbols to the output file mmap, and relocations
	// will be applied directly there.
	bench.Start("Asmb")
	asmb(ctxt)

	exitIfErrors()

	// Generate additional symbols for the native symbol table just prior
	// to code generation.
	bench.Start("GenSymsLate")
	if thearch.GenSymsLate != nil {
		thearch.GenSymsLate(ctxt, ctxt.loader)
	}

	bench.Start("Asmb2")
	asmb2(ctxt)

	bench.Start("Munmap")
	ctxt.Out.Close() // Close handles Munmapping if necessary.

	bench.Start("hostlink")
	ctxt.hostlink()
	if ctxt.Debugvlog != 0 {
		ctxt.Logf("%s", ctxt.loader.Stat())
		ctxt.Logf("%d liveness data\n", liveness)
	}
	bench.Start("Flush")
	ctxt.Bso.Flush()
	bench.Start("archive")
	ctxt.archive()
	bench.Report(os.Stdout)

	errorexit()
}

type Rpath struct {
	set bool
	val string
}

func (r *Rpath) Set(val string) error {
	r.set = true
	r.val = val
	return nil
}

func (r *Rpath) String() string {
	return r.val
}

func startProfile() {
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatalf("%v", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatalf("%v", err)
		}
		AtExit(pprof.StopCPUProfile)
	}
	if *memprofile != "" {
		if *memprofilerate != 0 {
			runtime.MemProfileRate = int(*memprofilerate)
		}
		f, err := os.Create(*memprofile)
		if err != nil {
			log.Fatalf("%v", err)
		}
		AtExit(func() {
			// Profile all outstanding allocations.
			runtime.GC()
			// compilebench parses the memory profile to extract memstats,
			// which are only written in the legacy pprof format.
			// See golang.org/issue/18641 and runtime/pprof/pprof.go:writeHeap.
			const writeLegacyFormat = 1
			if err := pprof.Lookup("heap").WriteTo(f, writeLegacyFormat); err != nil {
				log.Fatalf("%v", err)
			}
		})
	}
}
