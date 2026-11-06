import json,sys,urllib.request,re,glob,os,collections
rows=[l.split() for l in open(sys.argv[1]) if l.strip()]
pk=[(n,v.lstrip('v')) for n,v in rows if n!='tspike']
q={"queries":[{"package":{"name":n,"ecosystem":"crates.io"},"version":v} for n,v in pk]}
req=urllib.request.Request("https://api.osv.dev/v1/querybatch",data=json.dumps(q).encode(),headers={"Content-Type":"application/json","User-Agent":"spike"})
res=json.load(urllib.request.urlopen(req,timeout=60)).get("results",[])
hits=[(p,[v["id"] for v in r.get("vulns",[])]) for p,r in zip(pk,res) if r.get("vulns")]
print("osv queried",len(pk),"packages; with advisories:",len(hits))
for p,i in hits: print(" ",p,i)
reg=sys.argv[2]
lic=collections.Counter();miss=[];odd=[]
for n,v in pk:
    f=f"{reg}/{n}-{v}/Cargo.toml"
    if not os.path.exists(f): miss.append(n);continue
    m=re.search(r'^license\s*=\s*"([^"]+)"',open(f).read(),re.M)
    l=m.group(1) if m else "NONE";lic[l]+=1
    if re.search(r'GPL|AGPL|SSPL|NONE|BUSL',l) : odd.append((n,v,l))
print(lic.most_common());print("missing",miss);print("review",odd)
