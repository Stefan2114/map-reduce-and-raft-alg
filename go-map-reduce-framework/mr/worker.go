package mr

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/rpc"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Map functions return a slice of KeyValue.
type KeyValue struct {
	Key   string
	Value string
}

// ---------- Utility ----------

func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

// ---------- Worker Loop ----------

func Worker(mapFn func(string, string) []KeyValue, reduceFn func(string, []string) string) {
	for {
		resp := doHeartbeat()
		switch resp.JobType {
		case MapJob:
			doMapTask(mapFn, resp)
		case ReduceJob:
			doReduceTask(reduceFn, resp)
		case WaitJob:
			time.Sleep(time.Second)
		case CompleteJob:
			return
		default:
			panic(fmt.Sprintf("unexpected jobType: %v", resp.JobType))
		}
	}
}

// ---------- Map Phase ----------

func doMapTask(mapFn func(string, string) []KeyValue, resp HeartBeatResponse) {
	report := ReportRequest{TaskID: resp.TaskID, Success: false} //, Epoch: resp.Epoch}
	defer func() { reportCall(&report) }()

	file, err := os.Open(resp.FileName)
	if err != nil {
		log.Fatalf("cannot open %v: %v", resp.FileName, err)
	}
	content, _ := io.ReadAll(file)
	file.Close()

	kvArray := mapFn(resp.FileName, string(content))
	buckets := make([]*json.Encoder, resp.NReduce)
	files := make([]*os.File, resp.NReduce)

	for i := 0; i < resp.NReduce; i++ {
		tempName := fmt.Sprintf("mr-%d-%d-*", resp.TaskID, i)
		file, err := os.CreateTemp(".", tempName)
		if err != nil {
			log.Fatalf("cannot create temp file for %v", tempName)
		}
		files[i] = file
		buckets[i] = json.NewEncoder(file)
	}

	for _, kv := range kvArray {
		reduceID := ihash(kv.Key) % resp.NReduce
		_ = buckets[reduceID].Encode(&kv) // TODO: make validation
	}

	for i := 0; i < resp.NReduce; i++ {
		files[i].Close()
		finalName := fmt.Sprintf("mr-%d-%d", resp.TaskID, i)
		if err := os.Rename(files[i].Name(), finalName); err != nil {
			log.Fatalf("rename failed: %v", err)
		}
	}

	report.Success = true
}

// ---------- Reduce Phase ----------

func doReduceTask(reduceFn func(string, []string) string, resp HeartBeatResponse) {

	report := ReportRequest{TaskID: resp.TaskID, Success: false} //, Epoch: resp.Epoch}
	defer func() { reportCall(&report) }()

	reduceID := resp.TaskID
	allFiles, _ := filepath.Glob(fmt.Sprintf("mr-*-%d", reduceID))

	var files []string
	for _, file := range allFiles {
		if strings.Count(file, "-") == 2 { // skip temp files
			files = append(files, file)
		}
	}

	var kvArray []KeyValue
	for _, fname := range files {
		file, err := os.Open(fname)
		if err != nil {
			log.Fatalf("cannot open %v: %v", fname, err)
		}
		dec := json.NewDecoder(file)
		for {
			var kv KeyValue
			if err := dec.Decode(&kv); err != nil {
				break
			}
			kvArray = append(kvArray, kv)
		}
		file.Close()
	}

	kvMap := map[string][]string{}
	for _, kv := range kvArray {
		kvMap[kv.Key] = append(kvMap[kv.Key], kv.Value)
	}

	keys := make([]string, 0, len(kvMap))
	for k := range kvMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	finalName := fmt.Sprintf("mr-out-%v", reduceID)
	tempFile, err := os.CreateTemp(".", finalName+"-*")
	if err != nil {
		log.Fatalf("cannot create temp file for reduce %v: %v", reduceID, err)
	}

	for _, k := range keys {
		sf := reduceFn(k, kvMap[k])
		fmt.Fprintf(tempFile, "%v %v\n", k, sf)
	}
	err = tempFile.Close()
	if err != nil {
		log.Fatalf("cannot close temp file: %v", err)
	}

	// FIX: Atomically rename the temp file to the final name
	if err := os.Rename(tempFile.Name(), finalName); err != nil {
		log.Fatalf("cannot rename temp file: %v", err)
	}

	// fmt.Printf("ID: %v, Output closed\n", reduceID)

	for _, f := range files {
		_ = os.Remove(f)
	}

	report.Success = true
}

// ---------- RPC Helpers ----------

func doHeartbeat() HeartBeatResponse {
	args := HeartbeatRequest{}
	reply := HeartBeatResponse{}
	if !call("Coordinator.HandleHeartbeat", &args, &reply) {
		os.Exit(0)
	}
	return reply
}

func reportCall(args *ReportRequest) {
	reply := ReportResponse{}
	if !call("Coordinator.HandleReport", args, &reply) {
		os.Exit(0)
	}
}

func call(method string, args any, reply any) bool {
	sock := coordinatorSock()
	conn, err := rpc.DialHTTP("unix", sock)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer conn.Close()
	return conn.Call(method, args, reply) == nil
}
