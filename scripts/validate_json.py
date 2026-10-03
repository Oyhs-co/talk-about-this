#!/usr/bin/env python3
"""Valida que los archivos JSON de configuracion y especificacion sean JSON valido.

Uso:
    python scripts/validate_json.py docs/specifications/backlog_schema.json
    python scripts/validate_json.py            # valida los archivos por defecto

Se lee siempre en UTF-8 explicito para no depender de la codificacion de la
consola del sistema (cp1252 en Windows, utf-8 en Linux/macOS).
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

DEFAULT_FILES = [
    Path("docs/specifications/backlog_schema.json"),
    Path("configs/mappings.example.json"),
]

REQUIRED_TOP_LEVEL = {
    "docs/specifications/backlog_schema.json": ["type", "properties", "required"],
    "configs/mappings.example.json": ["version", "mappings"],
}


def validate(path: Path) -> list[str]:
    """Devuelve una lista de problemas encontrados en `path`. Vacia si es correcto."""
    problems: list[str] = []

    if not path.is_file():
        return [f"no existe el archivo {path}"]

    try:
        # utf-8-sig tolera tanto UTF-8 puro como UTF-8 con BOM.
        with path.open(encoding="utf-8-sig") as handle:
            data = json.load(handle)
    except json.JSONDecodeError as err:
        return [f"JSON invalido en {path}: linea {err.lineno}, columna {err.colno}: {err.msg}"]
    except UnicodeDecodeError as err:
        return [f"{path} no esta codificado en UTF-8: {err}"]

    if not isinstance(data, dict):
        return [f"{path}: la raiz debe ser un objeto JSON, no {type(data).__name__}"]

    expected = REQUIRED_TOP_LEVEL.get(path.as_posix())
    if expected:
        missing = [key for key in expected if key not in data]
        if missing:
            problems.append(f"{path}: faltan claves requeridas: {', '.join(missing)}")

    return problems


def main(argv: list[str]) -> int:
    files = [Path(arg) for arg in argv[1:]] or DEFAULT_FILES
    all_problems: list[str] = []

    for path in files:
        problems = validate(path)
        if problems:
            all_problems.extend(problems)
            print(f"FALLO  {path}")
        else:
            print(f"OK     {path}")

    if all_problems:
        print("\nSe encontraron problemas:", file=sys.stderr)
        for problem in all_problems:
            print(f"  - {problem}", file=sys.stderr)
        return 1

    print("\nTodos los archivos JSON son validos.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))