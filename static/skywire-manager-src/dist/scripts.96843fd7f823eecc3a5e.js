var Terminal=function(){var t,i=function(t,n){var e=n._cursor;setTimeout((function(){t.parentElement&&n._shouldBlinkCursor?(e.style.visibility="visible"===e.style.visibility?"hidden":"visible",i(t,n)):e.style.visibility="visible"}),500)},n=!0;return promptInput=function(e,s,o,u){var h=1===o;(t=document.createElement("input")).style.position="absolute",t.style.zIndex="-100",t.style.outline="none",t.style.border="none",t.style.opacity="0",t.style.fontSize="0.2em",e._inputLine.textContent="",e._input.style.display="block",e.html.appendChild(t),i(t,e),s.length&&e.print(3===o?s+" (y/n)":s,!0),t.onblur=function(){e._cursor.style.opacity=0},t.onfocus=function(){t.value=e._inputLine.textContent,e._cursor.style.display="inline",e._cursor.style.opacity=1},e.html.onclick=function(){t.focus()},t.onkeydown=function(i){37===i.which||39===i.which||38===i.which||40===i.which||9===i.which?i.preventDefault():h&&13!==i.which&&setTimeout((function(){e._inputLine.textContent=t.value}),1)},t.onkeyup=function(i){if(3===o||13===i.which){e._input.style.display="none";var n=t.value;h&&e.print(n),e.html.removeChild(t),t=void 0,"function"==typeof u&&u(3===o?"Y"===n.toUpperCase()[0]:n)}},n?(n=!1,setTimeout((function(){t.focus()}),50)):t.focus()},function(i){this.html=document.createElement("div"),this.html.className="Terminal","string"==typeof i&&(this.html.id=i),this._innerWindow=document.createElement("div"),this._output=document.createElement("p"),this._inputLine=document.createElement("span"),this._cursor=document.createElement("span"),this._input=document.createElement("p"),this._shouldBlinkCursor=!0,this.print=function(t,i){var n=document.createElement("div");n.innerHTML=t,this._output.appendChild(n),i&&(n.style.color="#00bd00")},this.input=function(t,i){promptInput(this,t,1,i)},this.changeInputContent=function(i){if(t&&this._inputLine)try{t.value=i,this._inputLine.textContent=i}catch(n){}},this.getInputContent=function(){return t?t.value:""},this.hasFocus=function(){return t&&document.activeElement===t},this.password=function(t,i){promptInput(this,t,2,i)},this.confirm=function(t,i){promptInput(this,t,3,i)},this.clear=function(){this._output.innerHTML=""},this.sleep=function(t,i){setTimeout(i,t)},this.setTextSize=function(t){this._output.style.fontSize=t,this._input.style.fontSize=t},this.setTextColor=function(t){this.html.style.color=t,this._cursor.style.background=t},this.setBackgroundColor=function(t){this.html.style.background=t},this.setWidth=function(t){this.html.style.width=t},this.setHeight=function(t){this.html.style.height=t},this.blinkingCursor=function(t){t=t.toString().toUpperCase(),this._shouldBlinkCursor="TRUE"===t||"1"===t||"YES"===t},this._input.appendChild(this._inputLine),this._input.appendChild(this._cursor),this._innerWindow.appendChild(this._output),this._innerWindow.appendChild(this._input),this.html.appendChild(this._innerWindow),this.setBackgroundColor("black"),this.setTextColor("white"),this.setTextSize("1em"),this.setWidth("100%"),this.setHeight("100%"),this.html.style.fontFamily="Monaco, Courier",this.html.style.margin="0",this._innerWindow.style.padding="10px",this._input.style.margin="0",this._output.style.margin="0",this._cursor.style.background="white",this._cursor.innerHTML="C",this._cursor.style.display="none",this._input.style.display="none"}}();