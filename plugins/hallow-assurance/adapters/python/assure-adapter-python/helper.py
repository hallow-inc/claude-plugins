import ast
import configparser
import io
import json
import os
import sys
import tokenize

FUNCS = (ast.FunctionDef, ast.AsyncFunctionDef)
DISCOVERY_KEYS = ("python_files", "python_classes", "python_functions")


def span(node):
    return min([node.lineno] + [d.lineno for d in node.decorator_list]), node.end_lineno


def test_entry(lines, node, cls):
    start, end = span(node)
    return {
        "class": cls,
        "name": node.name,
        "start": start,
        "end": end,
        "text": "".join(lines[start - 1 : end]),
    }


def scope(body):
    for node in body:
        if isinstance(node, FUNCS + (ast.ClassDef,)):
            yield node
        elif isinstance(node, ast.stmt):
            yield from scope(ast.iter_child_nodes(node))
        elif isinstance(node, (ast.excepthandler, ast.match_case)):
            yield from scope(node.body)


def functions(body, prefix, out):
    for node in scope(body):
        if isinstance(node, FUNCS):
            name = prefix + node.name
            out.append({"name": name, "start": node.lineno, "end": node.end_lineno})
            functions(node.body, name + ".", out)
        else:
            functions(node.body, prefix + node.name + ".", out)


def describe(path):
    with open(path, "rb") as f:
        source = f.read()
    try:
        tree = ast.parse(source, filename=path)
    except SyntaxError as e:
        return {"error": "%s:%s: %s" % (path, e.lineno, e.msg)}
    except ValueError as e:
        return {"error": "%s: %s" % (path, e)}
    encoding = tokenize.detect_encoding(io.BytesIO(source).readline)[0]
    lines = io.StringIO(source.decode(encoding), newline="").readlines()
    tests = []
    for node in scope(tree.body):
        if isinstance(node, FUNCS) and node.name.startswith("test"):
            tests.append(test_entry(lines, node, ""))
        elif isinstance(node, ast.ClassDef) and node.name.startswith("Test"):
            for child in scope(node.body):
                if isinstance(child, FUNCS) and child.name.startswith("test"):
                    tests.append(test_entry(lines, child, node.name))
    funcs = []
    functions(tree.body, "", funcs)
    return {"tests": tests, "functions": funcs}


def ini_keys(path, section):
    parser = configparser.ConfigParser(interpolation=None, strict=False)
    with open(path, encoding="utf-8") as f:
        parser.read_file(f)
    if not parser.has_section(section):
        return []
    return [k for k in DISCOVERY_KEYS if parser.has_option(section, k)]


def toml_keys(path):
    import tomllib

    with open(path, "rb") as f:
        tool = tomllib.load(f).get("tool", {})
    pytest = tool.get("pytest", {})
    tables = [pytest, pytest.get("ini_options", {})]
    return [k for k in DISCOVERY_KEYS if any(k in t for t in tables)]


def config(path):
    name = os.path.basename(path)
    try:
        if name == "pyproject.toml":
            keys = toml_keys(path)
        elif name == "setup.cfg":
            keys = ini_keys(path, "tool:pytest")
        else:
            keys = ini_keys(path, "pytest")
    except Exception as e:
        return {"error": "%s: %s" % (path, e)}
    return {"keys": keys}


def syntax(path):
    import parso

    with open(path, "rb") as f:
        source = f.read()
    encoding = tokenize.detect_encoding(io.BytesIO(source).readline)[0]
    grammar = parso.load_grammar()
    for e in grammar.iter_errors(grammar.parse(source.decode(encoding))):
        return {"error": "%s:%s: %s" % (path, e.start_pos[0], e.message)}
    return {}


mode, paths = sys.argv[1], sys.argv[2:]
handler = {"files": describe, "config": config, "syntax": syntax}[mode]
json.dump({p: handler(p) for p in paths}, sys.stdout)
