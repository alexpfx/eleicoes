#!/usr/bin/env python3
"""Acompanha em tempo real a apuração da eleição para Presidente, direto da API pública do TSE."""

import argparse
import os
import sys
import time
from datetime import datetime

import requests

BASE = "https://resultados.tse.jus.br/oficial"
CARGO_PRESIDENTE = 1
INTERVALO_MINIMO = 15
LARGURA_BARRA = 40

USAR_ANSI = sys.stdout.isatty() and "NO_COLOR" not in os.environ


def estilo(texto, codigo):
    return f"\033[{codigo}m{texto}\033[0m" if USAR_ANSI else texto


def fmt_int(valor):
    return f"{int(valor or 0):,}".replace(",", ".")


def pct_num(valor):
    return float((valor or "0").replace(",", "."))


def descobrir_eleicao(sessao, base, ciclo, turno):
    """Procura no ele-c.json a eleição do ciclo/turno que tem o cargo de Presidente."""
    resp = sessao.get(f"{base}/comum/config/ele-c.json", timeout=20)
    resp.raise_for_status()
    for pleito in resp.json().get("pl", []):
        if pleito.get("c") != ciclo:
            continue
        for ele in pleito.get("e", []):
            if ele.get("t") != str(turno):
                continue
            for abr in ele.get("abr", []):
                if any(cp.get("cd") == str(CARGO_PRESIDENTE) for cp in abr.get("cp", [])):
                    return int(ele["cd"]), ele.get("nm", "")
    return None


def url_resultado(base, ciclo, eleicao, uf):
    return f"{base}/{ciclo}/{eleicao}/dados/{uf}/{uf}-c{CARGO_PRESIDENTE:04d}-e{eleicao:06d}-u.json"


def buscar(sessao, url, etag):
    """Retorna (dados, etag). dados é None quando o arquivo não mudou (HTTP 304, sem corpo)."""
    headers = {"If-None-Match": etag} if etag else {}
    resp = sessao.get(url, headers=headers, timeout=20)
    if resp.status_code == 304:
        return None, etag
    resp.raise_for_status()
    return resp.json(), resp.headers.get("ETag")


def candidatos(dados):
    # Os candidatos ficam aninhados em cargo > agremiação > partido > candidato.
    lista = []
    for cargo in dados.get("carg", []):
        for agr in cargo.get("agr", []):
            for par in agr.get("par", []):
                for cand in par.get("cand", []):
                    lista.append({
                        "numero": cand.get("n", ""),
                        "nome": cand.get("nmu") or cand.get("nm", ""),
                        "partido": par.get("sg", ""),
                        "votos": int(cand.get("vap") or 0),
                        "pct": cand.get("pvap") or "0,00",
                        "pctn": pct_num(cand.get("pvapn")),
                        "situacao": cand.get("st", ""),
                        "seq": int(cand.get("seq") or 0),
                    })
    # Empate (ex.: todos com 0 votos antes da apuração) segue a ordem da urna.
    lista.sort(key=lambda c: (-c["votos"], c["seq"]))
    return lista


def renderizar(dados, titulo, uf, anteriores, aviso, proxima, simulacao=None):
    linhas = []
    if simulacao:
        linhas.append(estilo(f" SIMULAÇÃO — dados fictícios, não oficiais (fonte: {simulacao}) ", "1;30;43"))
    linhas.append(estilo(f"{titulo} — Presidente ({uf.upper()})", "1"))

    if dados is None:
        linhas.append("")
        linhas.append("Ainda não há dados disponíveis.")
    else:
        s, e, v = dados.get("s", {}), dados.get("e", {}), dados.get("v", {})
        origem = "pela simulação" if simulacao else "pelo TSE"
        linhas.append(f"Atualizado {origem} em {dados.get('dg', '?')} {dados.get('hg', '')}"
                      f"  ·  consultado às {datetime.now():%H:%M:%S}")

        pst = pct_num(s.get("pstn"))
        cheio = round(pst / 100 * LARGURA_BARRA)
        barra = "█" * cheio + "░" * (LARGURA_BARRA - cheio)
        linhas.append(f"Seções totalizadas: {fmt_int(s.get('st'))} de {fmt_int(s.get('ts'))}"
                      f" ({s.get('pst', '0,00')}%)  {barra}")
        linhas.append("")

        lista = candidatos(dados)
        largura_nome = max((len(f"{c['nome']} ({c['partido']})") for c in lista), default=0)
        for pos, c in enumerate(lista, 1):
            rotulo = f"{c['nome']} ({c['partido']})".ljust(largura_nome)
            barra = "█" * round(c["pctn"] / 100 * LARGURA_BARRA)
            linha = (f"{pos:>3}º  {c['numero']:>2}  {rotulo}  {fmt_int(c['votos']):>12}"
                     f"  {c['pct']:>6}%  {barra}")
            delta = c["votos"] - anteriores.get(c["numero"], c["votos"])
            if delta:
                linha += estilo(f"  {'+' if delta > 0 else '-'}{fmt_int(abs(delta))}", "32" if delta > 0 else "31")
            if c["situacao"]:
                cor = "1;32" if c["situacao"].lower() == "eleito" else "33"
                linha += "  " + estilo(f"[{c['situacao']}]", cor)
            linhas.append(linha)

        if len(lista) >= 2 and lista[0]["votos"] > 0:
            dif_votos = lista[0]["votos"] - lista[1]["votos"]
            dif_pp = f"{lista[0]['pctn'] - lista[1]['pctn']:.2f}".replace(".", ",")
            linhas.append("")
            linhas.append(f"Diferença entre 1º e 2º: {fmt_int(dif_votos)} votos ({dif_pp} p.p.)")

        linhas.append("")
        linhas.append(f"Votos válidos: {fmt_int(v.get('vv'))} ({v.get('pvvc', '0,00')}%)"
                      f"   Brancos: {fmt_int(v.get('vb'))} ({v.get('pvb', '0,00')}%)"
                      f"   Nulos: {fmt_int(v.get('tvn'))} ({v.get('ptvn', '0,00')}%)")
        linhas.append(f"Comparecimento: {fmt_int(e.get('c'))} ({e.get('pc', '0,00')}%)"
                      f"   Abstenção: {fmt_int(e.get('a'))} ({e.get('pa', '0,00')}%)"
                      f"   Eleitorado: {fmt_int(e.get('te'))}")
        if dados.get("tf") == "s":
            linhas.append(estilo("Totalização concluída.", "1"))

    if aviso:
        linhas.append("")
        linhas.append(estilo(f"Aviso: {aviso}", "31"))
    if proxima:
        linhas.append("")
        linhas.append(f"Próxima consulta em {proxima}s · Ctrl+C para sair")

    if USAR_ANSI:
        sys.stdout.write("\033[H\033[2J")
    print("\n".join(linhas), flush=True)


def argumentos():
    p = argparse.ArgumentParser(
        description="Acompanha em tempo real o resultado da eleição para Presidente (dados do TSE).")
    p.add_argument("-t", "--turno", type=int, choices=(1, 2), default=2, help="turno (padrão: 2)")
    p.add_argument("-u", "--uf", default="br",
                   help="abrangência: br (padrão), sigla do estado (ex.: sp) ou zz (exterior)")
    p.add_argument("-i", "--intervalo", type=int, default=60,
                   help=f"segundos entre consultas (padrão: 60, mínimo: {INTERVALO_MINIMO} no TSE)")
    p.add_argument("-1", "--uma-vez", action="store_true", help="consulta uma vez e sai")
    p.add_argument("--ciclo", default="ele2026", help="ciclo eleitoral (padrão: ele2026)")
    p.add_argument("--eleicao", type=int, help="código da eleição no TSE (pula a descoberta automática)")
    p.add_argument("--base", default=BASE,
                   help="URL base dos dados (padrão: TSE); para testar, a do simulacao/servidor.py")
    args = p.parse_args()
    args.uf = args.uf.lower()
    args.base = args.base.rstrip("/")
    if len(args.uf) != 2 or not args.uf.isalpha():
        p.error("--uf deve ter duas letras (ex.: br, sp, zz)")
    minimo = INTERVALO_MINIMO if args.base == BASE else 1
    if args.intervalo < minimo:
        p.error(f"--intervalo mínimo é {minimo} segundo(s)")
    return args


def main():
    args = argumentos()
    sessao = requests.Session()
    sessao.headers["User-Agent"] = "eleicoes-cli/1.0"

    simulacao = None if args.base == BASE else args.base
    fonte = "o TSE" if simulacao is None else "o servidor de simulação"

    if args.eleicao:
        url, titulo = url_resultado(args.base, args.ciclo, args.eleicao, args.uf), f"Eleição {args.eleicao}"
    else:
        url, titulo = None, f"{args.turno}º turno"
    dados, etag, anteriores = None, None, {}

    while True:
        aviso = ""
        try:
            # A descoberta fica dentro do laço para tentar de novo se a fonte estiver fora do ar.
            if url is None:
                achada = descobrir_eleicao(sessao, args.base, args.ciclo, args.turno)
                if achada is None:
                    sys.exit(f"Nenhuma eleição para Presidente encontrada no {args.turno}º turno"
                             f" do ciclo {args.ciclo}.")
                eleicao, titulo = achada
                url = url_resultado(args.base, args.ciclo, eleicao, args.uf)
            novos, etag = buscar(sessao, url, etag)
            if novos is not None:
                # Guarda os votos da versão anterior para mostrar quanto cada um ganhou.
                if dados is not None and novos.get("idg") != dados.get("idg"):
                    anteriores = {c["numero"]: c["votos"] for c in candidatos(dados)}
                dados = novos
        except requests.HTTPError as erro:
            if erro.response is not None and erro.response.status_code == 404:
                aviso = f"{fonte} ainda não publicou o arquivo de resultados desta eleição/abrangência."
            else:
                aviso = f"erro HTTP ao consultar {fonte}: {erro}"
        except ValueError:
            # Vem antes de RequestException: o JSONDecodeError do requests herda das duas.
            aviso = f"{fonte} enviou uma resposta inválida (JSON malformado)."
        except (requests.ConnectionError, requests.Timeout):
            aviso = f"{fonte} não respondeu; nova tentativa na próxima consulta."
        except requests.RequestException as erro:
            aviso = f"falha de conexão com {fonte}: {erro}"

        encerrar = args.uma_vez or (dados is not None and dados.get("tf") == "s")
        renderizar(dados, titulo, args.uf, anteriores, aviso, None if encerrar else args.intervalo,
                   simulacao)
        if encerrar:
            return 1 if dados is None else 0
        time.sleep(args.intervalo)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print("\nEncerrado.")
