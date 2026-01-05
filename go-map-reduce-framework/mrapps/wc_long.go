package main

import (
	"go-map-reduce-framework/mr"
	"strings"
	"time"
)

func Map(filename string, contents string) []mr.KeyValue {
	time.Sleep(15 * time.Second)
	words := strings.Fields(contents)
	kva := []mr.KeyValue{}
	for _, w := range words {
		kv := mr.KeyValue{Key: w, Value: "1"}
		kva = append(kva, kv)
	}
	return kva
}

func Reduce(key string, values []string) string {
	return strings.Join(values, ",")
}
