package rsyncopts

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type poptOption struct {
	longName  string
	shortName string
	argInfo   int
	arg       any // depends on argInfo
	val       int // 0 means don't return, just update arg
	// descrip    string
	// argDescrip string
}

func (o *poptOption) name() string {
	if o.longName == "" {
		return "-" + o.shortName
	}
	return "--" + o.longName
}

// see popt(3).
const (
	POPT_ARG_NONE          = iota // int; No argument expected
	POPT_ARG_STRING               // char*; No type checking to be performed
	POPT_ARG_INT                  // int; An integer argument is expected
	POPT_ARG_LONG                 // long; A long integer is expected
	POPT_ARG_INCLUDE_TABLE        // nest another option table
	POPT_ARG_CALLBACK             // call a function
	POPT_ARG_INTL_DOMAIN          // <not documented in popt(3)>
	POPT_ARG_VAL                  // int; Integer value taken from val
	POPT_ARG_FLOAT                // float; A float argument is expected
	POPT_ARG_DOUBLE               // double; A double argument is expected
	POPT_ARG_LONGLONG             // long long; A long long integer is expected
	POPT_ARG_MAINCALL      = 16 + 11
	POPT_ARG_ARGV          = 12
	POPT_ARG_SHORT         = 13
	POPT_ARG_BITSET        = 16 + 14
)

const POPT_ARG_MASK = 0x000000FF

const (
	POPT_ARGFLAG_OR = 0x08000000
)

const (
	POPT_BIT_SET = POPT_ARG_VAL | POPT_ARGFLAG_OR
)

type PoptError struct {
	Errno int32
	Err   error
}

func (pe *PoptError) Unwrap() error { return pe.Err }

func (pe *PoptError) Error() string { return pe.Err.Error() }

// TODO(later): turn these into sentinel error values.
// which stringify like poptStrerror().
const (
	POPT_ERROR_NOARG        = -10 // missing argument
	POPT_ERROR_BADOPT       = -11 // unknown option
	POPT_ERROR_UNWANTEDARG  = -12 // option does not take an argument
	POPT_ERROR_OPTSTOODEEP  = -13 // aliases nested too deeply
	POPT_ERROR_BADQUOTE     = -15 // error in parameter quoting
	POPT_ERROR_ERRNO        = -16 // errno set, use strerror(errno)
	POPT_ERROR_BADNUMBER    = -17 // invalid numeric value
	POPT_ERROR_OVERFLOW     = -18 // number too large or too small
	POPT_ERROR_BADOPERATION = -19 // mutually exclusive logical operations requested
	POPT_ERROR_NULLARG      = -20 // opt->arg should not be NULL
	POPT_ERROR_MALLOC       = -21 // memory allocation failed
	POPT_ERROR_BADCONFIG    = -22 // config file failed sanity test
)

type Context struct {
	// state
	table       []poptOption
	args        []string
	nextCharArg string
	nextArg     string

	// output
	Options       *Options
	RemainingArgs []string
}

func (pc *Context) findOption(longName, shortName string) *poptOption {
	for idx, opt := range pc.table {
		if longName != "" && opt.longName == longName {
			return &pc.table[idx]
		}
		if shortName != "" && opt.shortName == shortName {
			return &pc.table[idx]
		}
	}
	return nil
}

func (pc *Context) poptSaveInt(opt *poptOption, val int) bool {
	intPtr := opt.arg.(*int)
	if intPtr == nil {
		return false
	}
	if opt.argInfo&POPT_ARGFLAG_OR != 0 {
		*intPtr |= val
	} else {
		*intPtr = val
	}
	return true
}

func (pc *Context) poptSaveArg(opt *poptOption, nextArg string) int32 {
	argType := opt.argInfo & POPT_ARG_MASK
	switch argType {
	case POPT_ARG_INT:
		i, err := strconv.ParseInt(nextArg, 0, 64)
		if err != nil {
			return POPT_ERROR_BADNUMBER
		}
		if i < math.MinInt32 || i > math.MaxInt32 {
			return POPT_ERROR_OVERFLOW
		}
		pc.poptSaveInt(opt, int(i))
		return 0

	case POPT_ARG_STRING:
		stringPtr := opt.arg.(*string)
		if stringPtr == nil {
			return 0
		}
		*stringPtr = nextArg
		return 0
	}

	return POPT_ERROR_BADOPERATION
}

func (pc *Context) poptGetNextOpt() (int32, error) {
	var opt *poptOption
	for {
		var longArg string
		if pc.nextCharArg == "" && len(pc.args) == 0 {
			return -1, nil // done
		}
		if pc.nextCharArg == "" {
			// process next long option
			origOptString := pc.args[0]
			pc.args = pc.args[1:]
			if origOptString == "" {
				return -1, &PoptError{
					Errno: POPT_ERROR_BADOPT,
					Err:   fmt.Errorf("unknown option: origOptString empty"),
				}
			}
			if origOptString[0] != '-' || origOptString == "-" {
				pc.RemainingArgs = append(pc.RemainingArgs, origOptString)
				continue
			}
			before, after, found := strings.Cut(origOptString, "=")
			if found {
				longArg = after
			}
			// remove the one dash we ensured is present
			before = strings.TrimPrefix(before, "-")
			// a second dash is permitted
			before = strings.TrimPrefix(before, "-")
			opt = pc.findOption(before, "")
			if opt == nil {
				// try and parse it as a short option
				pc.nextCharArg = origOptString[1:]
				longArg = ""
			}
		}
		if pc.nextCharArg != "" {
			// process next short option
			opt = pc.findOption("", pc.nextCharArg[:1])
			if opt == nil {
				return -1, &PoptError{
					Errno: POPT_ERROR_BADOPT,
					Err:   fmt.Errorf("option %q not found", pc.nextCharArg[:1]),
				}
			}
			pc.nextCharArg = pc.nextCharArg[1:]
		}
		if opt == nil {
			// neither long nor short? how can we end up here?
			return -1, &PoptError{
				Errno: POPT_ERROR_BADOPT,
				Err:   fmt.Errorf("neither long nor short option found"),
			}
		}
		argType := opt.argInfo & POPT_ARG_MASK
		if argType == POPT_ARG_NONE || argType == POPT_ARG_VAL {
			if longArg != "" || strings.HasPrefix(pc.nextCharArg, "=") {
				return -1, &PoptError{
					Errno: POPT_ERROR_UNWANTEDARG,
					Err:   fmt.Errorf("option %s does not take an argument", opt.name()),
				}
			}
			if opt.arg != nil {
				val := 1
				if argType == POPT_ARG_VAL {
					val = opt.val
				}
				if !pc.poptSaveInt(opt, val) {
					return -1, &PoptError{
						Errno: POPT_ERROR_BADOPERATION,
						Err:   fmt.Errorf("poptSaveInt"),
					}
				}
			}
		} else {
			nextArg := longArg
			if longArg != "" {
			} else if pc.nextCharArg != "" {
				nextArg = strings.TrimPrefix(pc.nextCharArg, "=")
				pc.nextCharArg = ""
			} else {
				if len(pc.args) == 0 {
					return -1, &PoptError{
						Errno: POPT_ERROR_NOARG,
						Err:   fmt.Errorf("missing argument for option %s", opt.name()),
					}
				}
				nextArg = pc.args[0]
				pc.args = pc.args[1:]
			}
			pc.nextArg = nextArg
			if opt.arg != nil {
				if errno := pc.poptSaveArg(opt, nextArg); errno != 0 {
					return -1, &PoptError{
						Errno: errno,
						Err:   fmt.Errorf("poptSaveArg"),
					}
				}
			}
		}
		if opt.val != 0 && argType != POPT_ARG_VAL {
			return int32(opt.val), nil
		}
	}
}

func (pc *Context) poptGetOptArg() string {
	ret := pc.nextArg
	pc.nextArg = ""
	return ret
}
