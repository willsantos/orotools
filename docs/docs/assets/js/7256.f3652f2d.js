"use strict";(self.webpackChunkorotools_docs_site=self.webpackChunkorotools_docs_site||[]).push([["7256"],{6699(t,e,a){a.d(e,{diagram:()=>$});var r=a(7454),o=a(4924),i=a(6348),l=a(1177),s=a(1293),n=a(6827),c=a(8731),d=l.UI.packet,k=class{constructor(){this.packet=[],this.setAccTitle=l.SV,this.getAccTitle=l.iN,this.setDiagramTitle=l.ke,this.getDiagramTitle=l.ab,this.getAccDescription=l.m7,this.setAccDescription=l.EI}static{(0,n.K)(this,"PacketDB")}getConfig(){let t=(0,i.$t)({...d,...(0,l.zj)().packet});return t.showBits&&(t.paddingY+=10),t}getPacket(){return this.packet}pushWord(t){t.length>0&&this.packet.push(t)}clear(){(0,l.IU)(),this.packet=[]}},h=(0,n.K)((t,e)=>{(0,r.S)(t,e);let a=-1,o=[],i=1,{bitsPerRow:l}=e.getConfig();for(let{start:r,end:n,bits:c,label:d}of t.blocks){if(void 0!==r&&void 0!==n&&n<r)throw Error(`Packet block ${r} - ${n} is invalid. End must be greater than start.`);if((r??=a+1)!==a+1)throw Error(`Packet block ${r} - ${n??r} is not contiguous. It should start from ${a+1}.`);if(0===c)throw Error(`Packet block ${r} is invalid. Cannot have a zero bit field.`);for(n??=r+(c??1)-1,c??=n-r+1,a=n,s.R.debug(`Packet block ${r} - ${a} with label ${d}`);o.length<=l+1&&e.getPacket().length<1e4;){let[t,a]=p({start:r,end:n,bits:c,label:d},i,l);if(o.push(t),t.end+1===i*l&&(e.pushWord(o),o=[],i++),!a)break;({start:r,end:n,bits:c,label:d}=a)}}e.pushWord(o)},"populate"),p=(0,n.K)((t,e,a)=>{if(void 0===t.start)throw Error("start should have been set during first phase");if(void 0===t.end)throw Error("end should have been set during first phase");if(t.start>t.end)throw Error(`Block start ${t.start} is greater than block end ${t.end}.`);if(t.end+1<=e*a)return[t,void 0];let r=e*a-1,o=e*a;return[{start:t.start,end:r,label:t.label,bits:r-t.start},{start:o,end:t.end,label:t.label,bits:t.end-o}]},"getNextFittingBlock"),b={parser:{yy:void 0},parse:(0,n.K)(async t=>{let e=await (0,c.qg)("packet",t),a=b.parser?.yy;if(!(a instanceof k))throw Error("parser.parser?.yy was not a PacketDB. This is due to a bug within Mermaid, please report this issue at https://github.com/mermaid-js/mermaid/issues.");s.R.debug(e),h(e,a)},"parse")},f=(0,n.K)((t,e,a,r)=>{let i=r.db,s=i.getConfig(),{rowHeight:n,paddingY:c,bitWidth:d,bitsPerRow:k}=s,h=i.getPacket(),p=i.getDiagramTitle(),b=n+c,f=b*(h.length+1)-(p?0:n),u=d*k+2,$=(0,o.D)(e);for(let[t,e]of($.attr("viewBox",`0 0 ${u} ${f}`),(0,l.a$)($,f,u,s.useMaxWidth),h.entries()))g($,e,t,s);$.append("text").text(p).attr("x",u/2).attr("y",f-b/2).attr("dominant-baseline","middle").attr("text-anchor","middle").attr("class","packetTitle")},"draw"),g=(0,n.K)((t,e,a,{rowHeight:r,paddingX:o,paddingY:i,bitWidth:l,bitsPerRow:s,showBits:n})=>{let c=t.append("g"),d=a*(r+i)+i;for(let t of e){let e=t.start%s*l+1,a=(t.end-t.start+1)*l-o;if(c.append("rect").attr("x",e).attr("y",d).attr("width",a).attr("height",r).attr("class","packetBlock"),c.append("text").attr("x",e+a/2).attr("y",d+r/2).attr("class","packetLabel").attr("dominant-baseline","middle").attr("text-anchor","middle").text(t.label),!n)continue;let i=t.end===t.start,k=d-2;c.append("text").attr("x",e+(i?a/2:0)).attr("y",k).attr("class","packetByte start").attr("dominant-baseline","auto").attr("text-anchor",i?"middle":"start").text(t.start),i||c.append("text").attr("x",e+a).attr("y",k).attr("class","packetByte end").attr("dominant-baseline","auto").attr("text-anchor","end").text(t.end)}},"drawWord"),u={byteFontSize:"10px",startByteColor:"black",endByteColor:"black",labelColor:"black",labelFontSize:"12px",titleColor:"black",titleFontSize:"14px",blockStrokeColor:"black",blockStrokeWidth:"1",blockFillColor:"#efefef"},$={parser:b,get db(){return new k},renderer:{draw:f},styles:(0,n.K)(({packet:t}={})=>{let e=(0,i.$t)(u,t);return`
	.packetByte {
		font-size: ${e.byteFontSize};
	}
	.packetByte.start {
		fill: ${e.startByteColor};
	}
	.packetByte.end {
		fill: ${e.endByteColor};
	}
	.packetLabel {
		fill: ${e.labelColor};
		font-size: ${e.labelFontSize};
	}
	.packetTitle {
		fill: ${e.titleColor};
		font-size: ${e.titleFontSize};
	}
	.packetBlock {
		stroke: ${e.blockStrokeColor};
		stroke-width: ${e.blockStrokeWidth};
		fill: ${e.blockFillColor};
	}
	`},"styles")}}}]);