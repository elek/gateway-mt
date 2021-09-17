package linksharing

import (
	"fmt"
	"github.com/spacemonkeygo/monkit/v3"
	"regexp"
	"strings"
)

type FuncCallCounter struct {
	initialCounters map[int64]int64
	selector        func(f *monkit.Func) bool
}

func (r FuncCallCounter) Snapshot() {
	monkit.Default.Funcs(func(f *monkit.Func) {
		if r.selector(f) {
			f.Reset()
		}
	})
}

func (r FuncCallCounter) PrintMethods() {
	monkit.Default.Funcs(func(f *monkit.Func) {
		matched := "   "
		if r.selector(f) {
			matched = "***"
		}
		fmt.Println(matched + " " + f.FullName())
	})
}

func (r FuncCallCounter) Check(expectedCalls int64) error {
	counter := int64(0)
	usedFuncs := make([]string, 0)
	monkit.Default.Funcs(func(f *monkit.Func) {
		if r.selector(f) {
			calls := allCalls(f)
			if calls > 0 {
				usedFuncs = append(usedFuncs, fmt.Sprintf("%s (%d)", f.FullName(), calls))
			}
			counter += calls

		}
	})
	if counter != expectedCalls {
		return fmt.Errorf("Selected functions called %d times instead of %d [%s]",
			counter, expectedCalls, strings.Join(usedFuncs, ", "))
	}
	return nil
}

func allCalls(f *monkit.Func) int64 {
	i := f.Success()
	for _, count := range f.Errors() {
		i += count
	}
	return i
}

func FullNameEqual(name string) func(*monkit.Func) bool {
	return func(f *monkit.Func) bool {
		return f.FullName() == name
	}
}

func FullNamePattern(pattern string) func(*monkit.Func) bool {
	exp := regexp.MustCompile(pattern)
	return func(f *monkit.Func) bool {
		return exp.MatchString(f.FullName())
	}
}

func NewFuncCallCounter(selector func(*monkit.Func) bool) FuncCallCounter {
	m := FuncCallCounter{
		selector: selector,
	}
	m.Snapshot()
	return m
}
