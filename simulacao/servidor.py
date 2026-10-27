#!/usr/bin/env python3
"""Servidor local que imita o TSE para testar o programa antes da apuração real.

Terminal 1:  venv/bin/python simulacao/servidor.py --passo 10
Terminal 2:  ./eleicoes --base http://127.0.0.1:8000/oficial -i 5

Serve o 1º turno real (modelo/t1) e o 2º turno fictício (dados/passo-NN), para o BR,
cada estado e ZZ (exterior). A partir da primeira consulta ao 2º turno, os arquivos
avançam para a próxima fotografia a cada --passo segundos e, depois da última, ficam
parados nela (reinicie o servidor para recomeçar). Use no cliente um intervalo menor
que o --passo para não pular fotografias. Responde 304 quando o If-None-Match bate e
404 para qualquer outro caminho.
"""

import argparse
import hashlib
import json
import re
import sys
import time
from datetime import datetime
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit

PASTA = Path(__file__).resolve().parent
DADOS = PASTA / "dados"
TURNO1 = PASTA / "modelo" / "t1"
CAMINHO_CONFIG = "/oficial/comum/config/ele-c.json"
CAMINHO_RESULTADO = re.compile(r"^/oficial/(ele\d+)/(\d+)/dados/([a-z]{2})/\3-c0001-e(\d{6})-u\.json$")


class Tratador(BaseHTTPRequestHandler):
    def do_GET(self):
        arquivo = self.server.arquivo(urlsplit(self.path).path)
        if arquivo is None or not arquivo.exists():
            self.send_error(404)
            return

        corpo = arquivo.read_bytes()
        etag = f'"{hashlib.md5(corpo).hexdigest()}"'
        if self.headers.get("If-None-Match") == etag:
            self.send_response(304)
            self.send_header("ETag", etag)
            self.end_headers()
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(corpo)))
        self.send_header("ETag", etag)
        self.end_headers()
        self.wfile.write(corpo)

    def log_message(self, formato, *args):
        ultimo = len(self.server.passos) - 1
        sys.stderr.write(f"{datetime.now():%H:%M:%S} [passo {self.server.passo_atual():02d}/{ultimo:02d}]"
                         f" {formato % args}\n")


class Servidor(ThreadingHTTPServer):
    def __init__(self, endereco, passo):
        super().__init__(endereco, Tratador)
        self.passo = passo
        self.passos = sorted(p for p in DADOS.glob("passo-*") if p.is_dir())
        config = json.loads((DADOS / "ele-c.json").read_text(encoding="utf-8"))
        self.ciclo = config["pl"][0]["c"]
        self.eleicoes = {e["t"]: int(e["cd"]) for pl in config["pl"] for e in pl["e"]}
        self.inicio = None  # o relógio só começa na primeira consulta ao 2º turno

    def passo_atual(self):
        if self.inicio is None:
            return 0
        return min(int((time.monotonic() - self.inicio) // self.passo), len(self.passos) - 1)

    def arquivo(self, caminho):
        if caminho == CAMINHO_CONFIG:
            return DADOS / "ele-c.json"
        m = CAMINHO_RESULTADO.match(caminho)
        if not m or m[1] != self.ciclo or int(m[2]) != int(m[4]):
            return None
        eleicao, uf = int(m[2]), m[3]
        if eleicao == self.eleicoes.get("1"):
            return TURNO1 / f"{uf}.json"
        if eleicao == self.eleicoes.get("2"):
            if self.inicio is None:
                self.inicio = time.monotonic()
            return self.passos[self.passo_atual()] / f"{uf}.json"
        return None


def main():
    p = argparse.ArgumentParser(description="Servidor local que simula a apuração do TSE.")
    p.add_argument("-p", "--porta", type=int, default=8000, help="porta (padrão: 8000)")
    p.add_argument("--passo", type=float, default=15, help="segundos por fotografia (padrão: 15)")
    args = p.parse_args()
    if args.passo <= 0:
        p.error("--passo deve ser maior que zero")
    if not (DADOS / "ele-c.json").exists() or not any(DADOS.glob("passo-*")):
        sys.exit(f"Não há fotografias em {DADOS}. Rode antes: venv/bin/python simulacao/gerar.py")

    servidor = Servidor(("127.0.0.1", args.porta), args.passo)
    total = len(servidor.passos)
    print(f"Simulação em http://127.0.0.1:{args.porta}/oficial — {total} fotografias,"
          f" uma a cada {args.passo:g}s a partir da primeira consulta"
          f" (~{(total - 1) * args.passo:g}s até o fim). Ctrl+C para parar.",
          file=sys.stderr, flush=True)
    try:
        servidor.serve_forever()
    except KeyboardInterrupt:
        print("\nServidor encerrado.", file=sys.stderr)
    finally:
        servidor.server_close()


if __name__ == "__main__":
    main()
