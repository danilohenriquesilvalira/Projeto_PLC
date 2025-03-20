package plc

import (
	"math"
	"reflect"
)

// CompareValues compara dois valores de forma robusta, tratando números com tolerância.
// Se os valores forem numéricos, eles são convertidos para float64 e comparados com uma tolerância.
func CompareValues(old, new interface{}) bool {
	// Se ambos forem nil, são iguais.
	if old == nil && new == nil {
		return true
	}
	// Se apenas um for nil, são diferentes.
	if old == nil || new == nil {
		return false
	}

	// Se os tipos são exatamente iguais, para números usa tolerância.
	if reflect.TypeOf(old) == reflect.TypeOf(new) {
		switch old.(type) {
		case float32, float64:
			oldNum, okOld := toFloat64(old)
			newNum, okNew := toFloat64(new)
			if okOld && okNew {
				return math.Abs(oldNum-newNum) < 1e-6
			}
		}
		// Para os demais tipos, pode usar comparação direta.
		return old == new
	}

	// Se os tipos diferem, tenta converter ambos para float64 (para números).
	oldNum, okOld := toFloat64(old)
	newNum, okNew := toFloat64(new)
	if okOld && okNew {
		return math.Abs(oldNum-newNum) < 1e-6
	}

	// Fallback para comparação profunda.
	return reflect.DeepEqual(old, new)
}

// toFloat64 tenta converter um valor numérico para float64.
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}
