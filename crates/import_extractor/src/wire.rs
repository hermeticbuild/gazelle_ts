use crate::ts;
use import_extractor_proto::gazelle_ts::import_extractor as pb;
use prost::Message;
use rayon::prelude::*;

pub fn encode_response(resp: pb::Response) -> Vec<u8> {
    resp.encode_to_vec()
}

/// Decode a request frame and produce the encoded response bytes.
///
/// Returns an error response (encoded) when the input bytes don't decode as a `Request`.
pub fn dispatch(frame: &[u8]) -> Vec<u8> {
    let req = match pb::Request::decode(frame) {
        Ok(r) => r,
        Err(e) => {
            eprintln!("import_extractor: invalid request: {e}");
            return encode_response(pb::Response {
                id: 0,
                data: Some(pb::response::Data::Error(pb::ResponseError {
                    message: format!("invalid request: {e}"),
                })),
            });
        }
    };

    let id = req.id;
    let resp = match req.data {
        Some(pb::request::Data::TsQuery(ts_req)) => handle_ts(id, ts_req),
        None => pb::Response {
            id,
            data: Some(pb::response::Data::Error(pb::ResponseError {
                message: "missing request data".to_string(),
            })),
        },
    };

    encode_response(resp)
}

pub fn handle_ts(id: u32, req: pb::TsQueryRequest) -> pb::Response {
    let imports: Vec<pb::TsImportByFile> = req
        .files
        .par_iter()
        .filter_map(|file| match ts::extract_references_from_file(file) {
            Ok(refs) => Some(pb::TsImportByFile {
                file: file.clone(),
                import_paths: refs.imports,
                global_names: refs.globals,
                has_main: refs.has_main,
            }),
            Err(e) => {
                eprintln!("import_extractor: skipping {file}: {e}");
                None
            }
        })
        .collect();

    pb::Response {
        id,
        data: Some(pb::response::Data::TsResult(pb::TsResponseResult {
            imports,
        })),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn build_ts_request(id: u32, files: Vec<&str>) -> Vec<u8> {
        pb::Request {
            id,
            data: Some(pb::request::Data::TsQuery(pb::TsQueryRequest {
                files: files.into_iter().map(String::from).collect(),
            })),
        }
        .encode_to_vec()
    }

    fn decode(bytes: &[u8]) -> pb::Response {
        pb::Response::decode(bytes).expect("response decodes")
    }

    #[test]
    fn dispatch_returns_error_for_garbage_frame() {
        let resp = decode(&dispatch(&[0xff, 0xff, 0xff, 0xff]));
        assert_eq!(resp.id, 0);
        match resp.data {
            Some(pb::response::Data::Error(e)) => assert!(e.message.starts_with("invalid request")),
            _ => panic!("expected error variant"),
        }
    }

    #[test]
    fn dispatch_returns_error_when_data_oneof_is_missing() {
        let req = pb::Request { id: 7, data: None }.encode_to_vec();
        let resp = decode(&dispatch(&req));
        assert_eq!(resp.id, 7);
        match resp.data {
            Some(pb::response::Data::Error(e)) => assert_eq!(e.message, "missing request data"),
            _ => panic!("expected error variant"),
        }
    }

    #[test]
    fn dispatch_preserves_request_id_on_ts_query() {
        let req = build_ts_request(42, vec![]);
        let resp = decode(&dispatch(&req));
        assert_eq!(resp.id, 42);
        assert!(matches!(resp.data, Some(pb::response::Data::TsResult(_))));
    }

    #[test]
    fn handle_ts_skips_files_that_fail_to_read() {
        let resp = handle_ts(
            1,
            pb::TsQueryRequest {
                files: vec!["/nonexistent/file/that/cannot/be/read.ts".into()],
            },
        );
        match resp.data {
            Some(pb::response::Data::TsResult(r)) => assert!(r.imports.is_empty()),
            _ => panic!("expected ts_result"),
        }
    }

    #[test]
    fn handle_ts_returns_imports_for_real_file() {
        let dir = std::env::temp_dir().join("import_extractor_wire_test_ts");
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("a.ts");
        std::fs::write(&path, "import { x } from 'mod-a';\nimport 'mod-b';").unwrap();

        let resp = handle_ts(
            5,
            pb::TsQueryRequest {
                files: vec![path.to_string_lossy().into_owned()],
            },
        );
        match resp.data {
            Some(pb::response::Data::TsResult(r)) => {
                assert_eq!(r.imports.len(), 1);
                assert_eq!(r.imports[0].import_paths, vec!["mod-a", "mod-b"]);
            }
            _ => panic!("expected ts_result"),
        }
    }

    #[test]
    fn handle_ts_returns_main_detection() {
        let dir = std::env::temp_dir().join("import_extractor_wire_test_main");
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("main.ts");
        std::fs::write(
            &path,
            "function main(args: string[]) {}\nmain(process.argv.slice(2));",
        )
        .unwrap();

        let resp = handle_ts(
            6,
            pb::TsQueryRequest {
                files: vec![path.to_string_lossy().into_owned()],
            },
        );
        match resp.data {
            Some(pb::response::Data::TsResult(r)) => assert!(r.imports[0].has_main),
            _ => panic!("expected ts_result"),
        }
    }

    #[test]
    fn dispatch_full_roundtrip_ts() {
        let dir = std::env::temp_dir().join("import_extractor_wire_test_rt");
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("rt.ts");
        std::fs::write(&path, "import 'lib-x';").unwrap();

        let req = build_ts_request(13, vec![&path.to_string_lossy()]);
        let resp = decode(&dispatch(&req));
        assert_eq!(resp.id, 13);
        match resp.data {
            Some(pb::response::Data::TsResult(r)) => {
                assert_eq!(r.imports[0].import_paths, vec!["lib-x"]);
            }
            _ => panic!("expected ts_result"),
        }
    }
}
