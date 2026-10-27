#!/usr/bin/env python3
"""Gera fotografias FICTÍCIAS da apuração do 2º turno para Presidente, no formato do TSE.

Moldes em modelo/ (arquivos reais do TSE, baixados em 25/10/2026):
    t1/<uf>.json   1º turno já totalizado; dá a base de comparecimento, brancos, nulos
                   e preferência entre os dois candidatos em cada estado
    t2/<uf>.json   2º turno antes da apuração (estrutura, eleitorado e seções)

Grava em dados/:
    ele-c.json           configuração com as duas eleições (1º e 2º turno)
    passo-NN/<uf>.json   uma fotografia por passo para cada estado, ZZ (exterior) e BR

O BR de cada passo é a soma dos estados. Os números do 2º turno são inventados e não são
projeção de nada: servem só para testar o programa.
"""

import copy
import json
import random
import shutil
from datetime import datetime, timedelta
from pathlib import Path

PASTA = Path(__file__).resolve().parent
MODELO = PASTA / "modelo"
DADOS = PASTA / "dados"
CICLO = "ele2026"
SEMENTE = 2026
INICIO = datetime(2026, 10, 25, 17, 0, 0)

# (minutos após o início, % de seções totalizadas no país)
CRONOGRAMA = [
    (0, 0), (5, 0.4), (10, 2.1), (15, 6.8), (20, 14.5), (25, 24.0), (30, 34.6),
    (35, 45.2), (40, 55.0), (45, 63.7), (50, 71.3), (55, 77.9), (60, 83.4),
    (70, 89.6), (80, 93.8), (90, 96.5), (100, 98.2), (115, 99.2), (130, 99.7),
    (150, 99.93), (170, 100),
]

# Cada estado apura num ritmo próprio v, sorteado nesta faixa: com o país em F, o estado
# está em 1 - (1 - F) ** v (v = 1 acompanha o país; v > 1 vai mais rápido).
VELOCIDADE = (0.7, 2.5)

# A fatia de CANDIDATO_A em cada estado parte da proporção dele contra o outro no 1º turno.
# DERIVA desloca essa fatia ao longo da apuração (-DERIVA no começo, +DERIVA no fim),
# o que provoca uma virada no total do país.
CANDIDATO_A = "22"
DERIVA = 0.05
AJUSTE_COMPARECIMENTO = 0.99  # comparecimento do 2º turno em relação ao 1º


def pct(parte, total):
    return f"{100 * parte / total:.2f}".replace(".", ",") if total else "0,00"


def pctn(parte, total):
    if not total:
        return "0"
    return f"{100 * parte / total:.9f}".rstrip("0").rstrip(".").replace(".", ",")


def campo(chave, valor, total):
    """Monta o trio que o TSE usa: valor, percentual arredondado e percentual completo."""
    return {chave: str(valor), f"p{chave}": pct(valor, total), f"p{chave}n": pctn(valor, total)}


def partidos(dados):
    for cargo in dados["carg"]:
        for agr in cargo["agr"]:
            yield agr, agr["par"]


def votos_candidatos(dados):
    return {c["n"]: int(c["vap"]) for _, pars in partidos(dados) for p in pars for c in p["cand"]}


def ler(caminho):
    return json.loads(caminho.read_text(encoding="utf-8"))


def fotografia(modelo, i, momento, cont, votos, situacao):
    """Preenche uma cópia do molde. cont tem ts, st, te, est, c, vb, tvn e vnt."""
    d = copy.deepcopy(modelo)
    ts, st, te, est, c = cont["ts"], cont["st"], cont["te"], cont["est"], cont["c"]
    concluida = st == ts
    d["dg"], d["hg"] = f"{momento:%d/%m/%Y}", f"{momento:%H:%M:%S}"
    d["dt"], d["ht"] = (d["dg"], d["hg"]) if st else ("", "")
    d["idg"] = str(int(modelo["idg"]) + i)
    d["tf"] = "s" if concluida else "n"
    d["and"] = "f" if concluida else ("p" if st else "n")

    d["s"] = {"ts": str(ts), **campo("st", st, ts), **campo("snt", ts - st, ts),
              **campo("si", st, st), **campo("sni", 0, st),
              **campo("sa", st, st), **campo("sna", 0, st)}
    d["e"] = {"te": str(te), **campo("est", est, te), **campo("esnt", te - est, te),
              **campo("esi", est, est), **campo("esni", 0, est),
              **campo("esa", est, est), **campo("esna", 0, est),
              **campo("c", c, est), **campo("a", est - c, est)}
    vv = sum(votos.values())
    d["v"] = {"tv": str(c), **campo("vvc", vv, c), **campo("vv", vv, vv),
              **campo("vnom", vv, vv), **campo("van", 0, vv), **campo("vansj", 0, vv),
              **campo("vb", cont["vb"], c), **campo("tvn", cont["tvn"], c),
              **campo("vn", cont["tvn"] - cont["vnt"], cont["tvn"]),
              **campo("vnt", cont["vnt"], cont["tvn"]), "vsan": "0", "vscv": "0"}

    for agr, pars in partidos(d):
        for par in pars:
            for cand in par["cand"]:
                cand.update(campo("vap", votos[cand["n"]], vv))
                if situacao:
                    cand["e"], cand["st"] = situacao[cand["n"]]
            par["tvtn"] = par["tvan"] = str(sum(votos[c["n"]] for c in par["cand"]))
        if "tvtn" in agr:
            agr["tvtn"] = agr["tvan"] = str(sum(int(p["tvtn"]) for p in pars))
    return d


def gerar():
    rng = random.Random(SEMENTE)
    modelos = {p.stem: ler(p) for p in sorted((MODELO / "t2").glob("*.json"))}
    ufs = [uf for uf in modelos if uf != "br"]
    numeros = list(votos_candidatos(modelos["br"]))
    outro = next(n for n in numeros if n != CANDIDATO_A)

    base = {}
    for uf in ufs:
        t1, t2 = ler(MODELO / "t1" / f"{uf}.json"), modelos[uf]
        v1 = votos_candidatos(t1)
        tv1 = int(t1["v"]["tv"])
        base[uf] = {
            "ts": int(t2["s"]["ts"]), "te": int(t2["e"]["te"]),
            "comparecimento": int(t1["e"]["c"]) / int(t1["e"]["te"]) * AJUSTE_COMPARECIMENTO,
            "brancos": int(t1["v"]["vb"]) / tv1,
            "nulos": int(t1["v"]["tvn"]) / tv1,
            "nulos_tecnicos": int(t1["v"]["vnt"]) / max(int(t1["v"]["tvn"]), 1),
            "fatia": v1[CANDIDATO_A] / (v1[CANDIDATO_A] + v1[outro]),
            "velocidade": rng.uniform(*VELOCIDADE),
        }

    for antigo in DADOS.glob("passo-*"):
        shutil.rmtree(antigo) if antigo.is_dir() else antigo.unlink()
    DADOS.mkdir(exist_ok=True)

    # Cada passo totaliza um lote novo de seções em cada estado; os totais são a soma dos
    # lotes, então nenhum número diminui de uma fotografia para a seguinte.
    cont = {uf: dict.fromkeys(("st", "est", "c", "vb", "tvn", "vnt"), 0) for uf in ufs}
    votos = {uf: dict.fromkeys(numeros, 0) for uf in ufs}
    for i, (minutos, pct_pais) in enumerate(CRONOGRAMA):
        final = i == len(CRONOGRAMA) - 1
        momento = INICIO + timedelta(minutes=minutos, seconds=rng.randint(0, 59) if i else 0)
        passo = DADOS / f"passo-{i:02d}"
        passo.mkdir()

        for uf in ufs:
            b, k = base[uf], cont[uf]
            antes = k["est"] / b["te"]
            fracao = 1.0 if final else 1 - (1 - pct_pais / 100) ** b["velocidade"]
            k["st"] = min(b["ts"], round(b["ts"] * fracao))
            est = b["te"] if k["st"] == b["ts"] else round(b["te"] * fracao)

            lote_c = round((est - k["est"]) * b["comparecimento"] * rng.uniform(0.98, 1.02))
            lote_vb = round(lote_c * b["brancos"] * rng.uniform(0.9, 1.1))
            lote_tvn = round(lote_c * b["nulos"] * rng.uniform(0.9, 1.1))
            lote_vv = lote_c - lote_vb - lote_tvn
            meio = (antes + fracao) / 2
            fatia = b["fatia"] + DERIVA * (2 * meio - 1) + rng.uniform(-0.004, 0.004)
            lote_a = round(lote_vv * fatia)

            k["est"] = est
            k["c"] += lote_c
            k["vb"] += lote_vb
            k["tvn"] += lote_tvn
            k["vnt"] += round(lote_tvn * b["nulos_tecnicos"])
            votos[uf][CANDIDATO_A] += lote_a
            votos[uf][outro] += lote_vv - lote_a

        total = {n: sum(votos[uf][n] for uf in ufs) for n in numeros}
        situacao = None
        if final:
            vencedor = max(total, key=total.get)
            situacao = {n: ("s", "Eleito") if n == vencedor else ("n", "Não eleito") for n in numeros}

        br = {chave: sum(cont[uf][chave] for uf in ufs) for chave in cont[ufs[0]]}
        br["ts"] = sum(base[uf]["ts"] for uf in ufs)
        br["te"] = sum(base[uf]["te"] for uf in ufs)
        arquivos = {"br": fotografia(modelos["br"], i, momento, br, total, situacao)}
        for uf in ufs:
            k = {**cont[uf], "ts": base[uf]["ts"], "te": base[uf]["te"]}
            arquivos[uf] = fotografia(modelos[uf], i, momento, k, votos[uf], situacao)
        for uf, d in arquivos.items():
            (passo / f"{uf}.json").write_text(
                json.dumps(d, ensure_ascii=False, separators=(",", ":")) + "\n", encoding="utf-8")

    t1_br = ler(MODELO / "t1" / "br.json")
    eleicao = {"tp": "8", "abr": [{"cd": "br", "cp": [{"cd": "1", "ds": "Presidente", "tp": "1"}]}]}
    config = {
        "dg": f"{momento:%d/%m/%Y}", "hg": f"{momento:%H:%M:%S}", "f": "o",
        "pl": [
            {"cd": "3220", "c": CICLO, "dt": t1_br["dg"], "e": [{
                **eleicao, "cd": t1_br["ele"], "cdt2": modelos["br"]["ele"], "t": "1",
                "nm": "SIMULAÇÃO (1º turno real) - Eleição Ordinária Federal - 2026 1º Turno"}]},
            {"cd": "3221", "c": CICLO, "dt": f"{INICIO:%d/%m/%Y}", "e": [{
                **eleicao, "cd": modelos["br"]["ele"], "cdt2": "", "t": "2",
                "nm": "SIMULAÇÃO (dados fictícios) - Eleição Ordinária Federal - 2026 2º Turno"}]},
        ],
    }
    (DADOS / "ele-c.json").write_text(
        json.dumps(config, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    print(f"{len(CRONOGRAMA)} passos × {len(arquivos)} arquivos gravados em {DADOS}")
    for n, v in sorted(total.items(), key=lambda x: -x[1]):
        print(f"  {n}: {f'{v:,}'.replace(',', '.')} ({pct(v, sum(total.values()))}%)")


if __name__ == "__main__":
    gerar()
