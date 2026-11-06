import json,sys
def load(p):
    out={};meta={}
    try:
        for l in open(p):
            l=l.strip()
            if not l.startswith('{'): continue
            try: d=json.loads(l)
            except Exception: continue
            if 'class' in d: out[d['class']]=d
            else: meta[d.get('stage','?')]=d
    except FileNotFoundError: pass
    return out,meta
sets=sys.argv[1:]
cols=[]
for s in sets:
    name,p=s.split('=');cols.append((name,*load(p)))
cl=[]
for _,o,_m in cols:
    for k in o:
        if k not in cl: cl.append(k)
print('%-24s'%'class'+''.join('%22s'%c[0] for c in cols))
for k in cl:
    r='%-24s'%k
    for _,o,_m in cols:
        d=o.get(k)
        r+='%22s'%('-' if not d else '%.2f/%.2f n=%d e=%d'%(d['p50_ms'],d['p95_ms'],d['n'],d.get('errors',0)))
    print(r)
print('hits')
for k in cl:
    print('%-24s'%k+''.join('%22s'%str((o.get(k) or {}).get('median_hits','-')) for _,o,_m in cols))
for n,_o,m in cols:
    if 'open' in m: print(n,'open',{k:m['open'].get(k) for k in('open_s','docs','segments','VmHWM_MiB','VmRSS_MiB')})
    for st,d in m.items():
        if st not in('open',): print(n,st,{k:v for k,v in d.items() if k not in('engine','stage')})
