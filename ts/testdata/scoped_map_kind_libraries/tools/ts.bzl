def app_ts_library(name, srcs, deps = [], **kwargs):
    native.filegroup(name = name, srcs = srcs + deps, **kwargs)

def node_ts_library(name, srcs, deps = [], **kwargs):
    native.filegroup(name = name, srcs = srcs + deps, **kwargs)
