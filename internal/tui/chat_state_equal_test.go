package tui

import "reflect"

func chatStatesEqual(left, right chatState) bool {
	return reflect.DeepEqual(left, right)
}
