// Package-internal cgo bridge to the Rust import_extractor library.
//
// The crate at //crates/import_extractor exposes a 2-function C ABI
// (gazelle_ts_ie_dispatch / gazelle_ts_ie_free) wrapping the protobuf wire
// dispatcher. We marshal a Request, hand the bytes to gazelle_ts_ie_dispatch,
// unmarshal the Response, and free the buffer the Rust side allocated.
package ts

/*
#include <stddef.h>
#include <stdint.h>

void gazelle_ts_ie_dispatch(
    const uint8_t *req_ptr,
    size_t req_len,
    uint8_t **out_resp_ptr,
    size_t *out_resp_len);

void gazelle_ts_ie_free(uint8_t *ptr, size_t len);
*/
import "C"

import (
	"fmt"
	"unsafe"

	pb "github.com/hermeticbuild/gazelle_ts/ts/proto"

	"google.golang.org/protobuf/proto"
)

type extractedTSFile struct {
	ImportPaths []string
	GlobalNames []string
	HasMain     bool
}

// extractImports sends a batch of file paths and returns parsed TypeScript
// references keyed by file path. Files that fail to parse are silently dropped
// by the Rust side.
func extractImports(files []string) (map[string]extractedTSFile, error) {
	req := &pb.Request{
		Data: &pb.Request_TsQuery{
			TsQuery: &pb.TsQueryRequest{Files: files},
		},
	}
	resp, err := dispatch(req)
	if err != nil {
		return nil, err
	}
	switch d := resp.Data.(type) {
	case *pb.Response_Error:
		return nil, fmt.Errorf("import-extractor: %s", d.Error.Message)
	case *pb.Response_TsResult:
		out := make(map[string]extractedTSFile, len(d.TsResult.Imports))
		for _, fi := range d.TsResult.Imports {
			out[fi.File] = extractedTSFile{
				ImportPaths: fi.ImportPaths,
				GlobalNames: fi.GlobalNames,
				HasMain:     fi.HasMain,
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("import-extractor: empty response oneof")
	}
}

func dispatch(req *pb.Request) (*pb.Response, error) {
	reqBytes, err := proto.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	var reqPtr *C.uint8_t
	if len(reqBytes) > 0 {
		reqPtr = (*C.uint8_t)(unsafe.Pointer(&reqBytes[0]))
	}

	var respPtr *C.uint8_t
	var respLen C.size_t
	C.gazelle_ts_ie_dispatch(reqPtr, C.size_t(len(reqBytes)), &respPtr, &respLen)

	if respPtr == nil || respLen == 0 {
		return nil, fmt.Errorf("import-extractor: empty response from FFI")
	}
	defer C.gazelle_ts_ie_free(respPtr, respLen)

	respBytes := C.GoBytes(unsafe.Pointer(respPtr), C.int(respLen))
	var resp pb.Response
	if err := proto.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &resp, nil
}
