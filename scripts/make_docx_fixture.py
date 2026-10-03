#!/usr/bin/env python3
"""Genera un fixture .docx valido (contenedor OOXML) para los tests.

Los .docx reales son archivos ZIP que contienen, entre otras partes,
word/document.xml. Este script construye uno minimo pero estructuralmente
correcto, con parrafos, negritas, listas y una tabla, de modo que los tests
del parser cubran los casos que aparecen en una minuta real.

No requiere dependencias externas: solo la biblioteca estandar zipfile.

Uso:
    python scripts/make_docx_fixture.py testdata/minuta.docx
"""

from __future__ import annotations

import sys
import zipfile
from pathlib import Path
from xml.sax.saxutils import escape

# Namespaces OOXML minimos para WordprocessingML.
W_NS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
CT_MAIN = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
CT_RELS = "application/vnd.openxmlformats-package.relationships+xml"
REL_MAIN = (
    "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
)

CONTENT_TYPES = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="{CT_RELS}"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="{CT_MAIN}"/>
</Types>
"""

RELS = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="{REL_MAIN}" Target="word/document.xml"/>
</Relationships>
"""


def parrafo(textos: list[tuple[str, bool]]) -> str:
    """Construye un <w:p>.

    `textos` es una lista de (texto, negrita). Varios fragmentos por parrafo
    permiten reproducir negritas dentro de una misma frase, como "Nota:" en
    negrita seguida del resto en normal.
    """
    runs = []
    for texto, negrita in textos:
        negrita_xml = "<w:b/>" if negrita else ""
        # xml:space="preserve" es imprescindible: sin el, Word (y el parser)
        # colapsarian los espacios significativos al inicio y final del run.
        runs.append(
            f'<w:r>{negrita_xml}<w:t xml:space="preserve">{escape(texto)}</w:t></w:r>'
        )
    return "<w:p>" + "".join(runs) + "</w:p>"


def elemento_vacio(etiqueta: str) -> str:
    """Salto de linea o tabulador dentro de un parrafo."""
    return f"<w:r><w:{etiqueta}/></w:r>"


def construir_documento() -> str:
    parrafos = [
        # Titulo con negrita.
        parrafo([("Acta de la reunion de arquitectura", True)]),
        parrafo([("Fecha: 15 de octubre de 2026", False)]),
        parrafo([("Asistentes: Omar Hernandez, Ana Maria Ruiz, Luis Cabrera", False)]),
        # Parrafo con negrita en medio y salto de linea explicito.
        "<w:p><w:r><w:t xml:space=\"preserve\">Nota: </w:t></w:r>"
        "<w:r><w:b/><w:t xml:space=\"preserve\">decision tomada</w:t></w:r>"
        "<w:r><w:br/><w:t xml:space=\"preserve\">Se descarta la migracion a la base de datos nueva.</w:t></w:r></w:p>",
        # Elementos con tabulador.
        "<w:p><w:r><w:t xml:space=\"preserve\">Responsable:</w:t></w:r>"
        f"{elemento_vacio('tab')}"
        "<w:r><w:t xml:space=\"preserve\">Omar Hernandez</w:t></w:r></w:p>",
        # Lista con sangria.
        "<w:p><w:pPr><w:ind w:left=\"720\"/></w:pPr>"
        "<w:r><w:t xml:space=\"preserve\">- Migrar la sesion fuera del contexto global</w:t></w:r></w:p>",
        "<w:p><w:pPr><w:ind w:left=\"720\"/></w:pPr>"
        "<w:r><w:t xml:space=\"preserve\">- Documentar los reintentos del webhook</w:t></w:r></w:p>",
        # Parrafo vacio, para comprobar que se ignoran al limpiar.
        "<w:p/>",
        # Acentos y enye, para comprobar UTF-8 real.
        parrafo([("Se revisa de nuevo en el proximo trimestre. Costo: 50000 MXN. "
                  "Se culpó a la configuración anterior.", False)]),
    ]

    return (
        '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
        f'<w:document xmlns:w="{W_NS}"><w:body>'
        + "".join(parrafos)
        + "</w:body></w:document>"
    )


def main() -> int:
    destino = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("testdata/minuta.docx")

    destino.parent.mkdir(parents=True, exist_ok=True)

    with zipfile.ZipFile(destino, "w", zipfile.ZIP_DEFLATED) as zf:
        # La primera entrada debe ser [Content_Types].xml por convencion OOXML.
        zf.writestr("[Content_Types].xml", CONTENT_TYPES)
        zf.writestr("_rels/.rels", RELS)
        zf.writestr("word/document.xml", construir_documento())

    print(f"OK -> {destino} ({destino.stat().st_size} bytes)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())