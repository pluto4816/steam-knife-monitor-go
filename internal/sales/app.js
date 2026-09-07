'use strict';
const form=document.getElementById('lot-form');
if(form){
 const money=s=>{if(!/^\d+(\.\d{1,2})?$/.test(s))throw Error();const [w,f='']=s.split('.');return BigInt(w)*100n+BigInt(f.padEnd(2,'0'));};
 const fmt=n=>(n/100n)+'.'+(n%100n).toString().padStart(2,'0');
 const update=()=>{const cost=document.getElementById('cost-preview'),target=document.getElementById('target-preview');cost.textContent="填写数量与单价，核对买入总成本";try{const q=BigInt(form.elements.namedItem("quantity").value),p=money(form.elements.namedItem("unit_cost").value);if(q<1n||p<1n)throw Error();cost.textContent=`${q} 件 × ¥${fmt(p)} = 买入总成本 ¥${fmt(q*p)}`;const t=form.elements.namedItem("target").value;if(!/^\d+(\.\d{1,4})?$/.test(t))throw Error();const [w,f='']=t.split('.'),bp=BigInt(w)*10000n+BigInt(f.padEnd(4,'0'));if(bp<1n)throw Error();target.textContent='目标 Steam 售价至少 ¥'+fmt((p*11500n+bp-1n)/bp)+' / 件';}catch{target.textContent='请填写有效数量、单价和目标比例';}};
 form.addEventListener('input',update);update();
 document.getElementById('item-name').addEventListener('change',e=>{const choices=Array.from(document.querySelectorAll('#items option')).filter(o=>o.value===e.target.value);if(choices.length===1)document.getElementById('hash-name').value=choices[0].dataset.hash;});
}
