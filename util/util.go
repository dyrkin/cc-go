package util

import (
	"fmt"
	"math"
	"reflect"
)

func FirstBitPosition(n uint64) uint8 {
	return uint8(math.Log2(float64(n & -n)))
}

func UintToHexString(v interface{}) (string, error) {
	m := reflect.ValueOf(v)
	if m.Kind() == reflect.Ptr {
		return UintToHexString(m.Elem().Interface())
	}
	switch m.Kind() {
	case reflect.Uint8:
		return fmt.Sprintf("0x%02x", v), nil
	case reflect.Uint16:
		return fmt.Sprintf("0x%04x", v), nil
	case reflect.Uint32:
		return fmt.Sprintf("0x%08x", v), nil
	case reflect.Uint64:
		return fmt.Sprintf("0x%016x", v), nil
	}
	return "", fmt.Errorf("reflection.Vtype: Unsupported value: %s", v)
}

func Panicf(format string, v ...interface{}) {
	panic(fmt.Sprintf(format, v...))
}
