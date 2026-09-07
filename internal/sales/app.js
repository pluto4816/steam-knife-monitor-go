'use strict';
const form=document.getElementById('lot-form');
if(form){
 const money=s=>{if(!/^\d+(\.\d{1,2})?$/.test(s))throw Error();const [w,f='']=s.split('.');return BigInt(w)*100n+BigInt(f.padEnd(2,'0'));};
 const fmt=n=>(n/100n)+'.'+(n%100n).toString().padStart(2,'0');
 const update=()=>{const cost=document.getElementById('cost-preview'),target=document.getElementById('target-preview');cost.textContent="填写数量与单价，核对买入总成本";try{const q=BigInt(form.elements.namedItem("quantity").value),p=money(form.elements.namedItem("unit_cost").value);if(q<1n||p<1n)throw Error();cost.textContent=`${q} 件 × ¥${fmt(p)} = 买入总成本 ¥${fmt(q*p)}`;const t=form.elements.namedItem("target").value;if(!/^\d+(\.\d{1,4})?$/.test(t))throw Error();const [w,f='']=t.split('.'),bp=BigInt(w)*10000n+BigInt(f.padEnd(4,'0'));if(bp<1n)throw Error();target.textContent='目标 Steam 售价至少 ¥'+fmt((p*11500n+bp-1n)/bp)+' / 件';}catch{target.textContent='请填写有效数量、单价和目标比例';}};
 form.addEventListener('input',update);update();
 const input=document.getElementById('item-name'), results=document.getElementById('item-results'), status=document.getElementById('item-search-status');
 const selected=document.getElementById('catalog-item-id'), hash=document.getElementById('hash-name');
 const choices=Array.from(results.querySelectorAll('button'));
 let visible=[],active=-1,composing=false;
 const close=()=>{results.hidden=true;input.setAttribute('aria-expanded','false');input.removeAttribute('aria-activedescendant');active=-1;};
 const choose=button=>{input.value=button.textContent;selected.value=button.dataset.id;hash.value=button.dataset.hash;input.setCustomValidity('');close();status.textContent='已关联：'+button.textContent;input.focus();};
 choices.forEach((button,index)=>{button.id='item-option-'+index;button.tabIndex=-1;button.addEventListener('click',()=>choose(button));});
 const search=()=>{
  selected.value='';hash.value='';input.setCustomValidity('');active=-1;input.removeAttribute('aria-activedescendant');
  const query=input.value.trim().toLowerCase(),terms=query.split(/\s+/);
  visible=Array.from(query).length<2?[]:choices.filter(button=>terms.every(term=>(button.textContent+' '+button.dataset.hash).toLowerCase().includes(term)));
  choices.forEach(button=>{button.hidden=!visible.includes(button);button.setAttribute('aria-selected','false');});
  results.hidden=visible.length===0;input.setAttribute('aria-expanded',String(visible.length>0));
  status.textContent=visible.length?'找到 '+visible.length+' 个物品，请点击选择。':query.length>=2?(choices.length?'没有匹配项，可继续输入或手动填写完整名称。':'候选目录尚未载入，当前使用手动填写。'):'';
 };
 input.addEventListener('compositionstart',()=>{composing=true;});
 input.addEventListener('compositionend',()=>{composing=false;search();});
 input.addEventListener('input',()=>{if(!composing)search();});
 input.addEventListener('keydown',event=>{
  if(composing||event.isComposing)return;
  if(event.key==='Escape'){close();return;}
  if(results.hidden)return;
  if(event.key==='ArrowDown'||event.key==='ArrowUp'){
   event.preventDefault();active=active<0?(event.key==='ArrowDown'?0:visible.length-1):(active+(event.key==='ArrowDown'?1:-1)+visible.length)%visible.length;
   visible.forEach((button,i)=>button.setAttribute('aria-selected',String(i===active)));
   input.setAttribute('aria-activedescendant',visible[active].id);visible[active].scrollIntoView({block:'nearest'});
  }else if(event.key==='Enter'){
   event.preventDefault();if(active>=0)choose(visible[active]);else if(visible.length===1)choose(visible[0]);
  }
 });
 form.addEventListener('submit',event=>{
  if(!selected.value&&visible.length&&input.value.trim()!==visible[0].textContent){
   event.preventDefault();input.setCustomValidity('请点击搜索结果确认物品');input.reportValidity();
  }
 });
}
