## Context

Ver `proposal.md` para la motivacion y `specs/` para los requisitos.

Lo que ya esta hecho y condiciona el resto:

- **El bus existe** (`events/`), con ring buffer, `seq` monotonico, `Replay(afterSeq)` y
  `Subscribe(notify)`. El contrato de eventos quedo congelado en `01-add-relay-event-bus`
  justamente para que la pagina de Node se pueda portar sin tocarla.
- **El ruteo por path existe** (`02-add-relay-http-endpoints`): las rutas se registran por su path
  y el metodo se comprueba dentro del manejador, porque el catch-all se lleva cualquier patron con
  metodo.
- **`dashboard.enabled` y `dashboard.bufferSize` ya se leen** de `config.toml`, y con el flag
  apagado el bus es inerte y publicar no cuesta nada.

Y una diferencia con Node que no es cosmetica: alla `replay()` y `subscribe()` corren sin nada en
el medio porque el event loop no cede el control. **En Go no.** `Replay` y `Subscribe` toman el
mutex del bus por separado, asi que entre las dos llamadas otra goroutine puede publicar: ese
evento no sale en el replay y todavia no hay suscripcion que lo reciba. Se pierde, en silencio, y
justo cuando mas importa —en una rafaga—.

## Goals / Non-Goals

**Goals:**

- Ver en vivo lo que le pasa a cada metatx, con reanudacion que no deje huecos.
- Que observar no pueda afectar al camino de la metatx, ni por lentitud, ni por cantidad, ni por
  una desconexion abrupta.
- Portar la pagina de Node sin modificarla.
- Que con el dashboard apagado no exista ninguna superficie nueva.

**Non-Goals:**

- Autenticacion. El monitor no la tiene, como el resto del servicio, y por eso el default es
  apagado y por eso queda fuera del puerto 80.
- Persistir eventos o agregarlos entre instancias: lo que se ve es lo que vio este proceso.
- Cambiar el vocabulario de eventos. Esta congelado, y la pagina depende de eso.
- Abrir el monitor por el puerto 80: exige tocar la plantilla de `besu-networks`, que va aparte.

## Decisions

### D1. El bus gana una operacion atomica de reanudacion

Es la decision que hace cumplible el requisito de "ni huecos ni repetidos", y no se resuelve del
lado del manejador.

```
   sin operacion atomica                     con ReplayAndSubscribe
   ---------------------                     ----------------------

   Replay(after)      [toma y suelta lock]   ReplayAndSubscribe(after, notify)
        |                                         [toma el lock]
        |  <-- otra goroutine publica aqui          copia lo retenido
        |      y ese evento se pierde               registra la suscripcion
        v                                         [suelta el lock]
   Subscribe(notify)  [toma y suelta lock]
```

Se agrega al bus una operacion que hace las dos cosas **bajo el mismo candado**: copia lo retenido
posterior a `afterSeq` y registra la suscripcion antes de soltarlo. No hay instante en que un
evento no pertenezca ni al historial ni al flujo.

Alternativas descartadas:

| Opcion | Por que no |
|---|---|
| Suscribir primero y despues replay, descartando por `seq` | funciona, pero mueve la correccion al manejador y obliga a cada consumidor futuro a acordarse de deduplicar |
| Dejarlo como esta y aceptar el hueco | es exactamente el fallo silencioso que el monitor existe para evitar |

No cambia ningun requisito de `relay-event-stream`: `Replay` y `Subscribe` siguen existiendo tal
cual, y esto es una forma combinada de las dos.

### D2. Server-Sent Events, no WebSocket

El flujo es de una sola direccion: el navegador no manda nada. SSE va sobre HTTP comun —el puerto
ya esta abierto—, el navegador reconecta solo, y trae la reanudacion por identificador de evento,
que es exactamente lo que el `seq` del bus necesita.

Ademas, el WebSocket de este proceso ya se usa **hacia el nodo**, para resetear el cupo de gas por
bloque. Agregar un WebSocket de cara al cliente mezclaria dos usos distintos en el mismo proceso.

### D3. La pagina viaja dentro del binario

Se porta `dashboard-page.ts` a un `index.html` y se embebe con `go:embed`. Sin build, sin
dependencias de JavaScript, sin archivos al lado del ejecutable.

El motivo es de operacion: un despliegue aca es reemplazar un binario. Una pagina servida desde el
sistema de archivos se rompe en cuanto alguien copia solo el ejecutable, y el fallo aparece recien
cuando se abre el monitor, que es cuando algo ya anda mal.

La pagina **no se modifica al portarla**. Consume el vocabulario congelado en `01`, y cualquier
ajuste que se le hiciera seria una divergencia silenciosa contra el frontend de referencia. Si algo
no pinta, el defecto esta en lo que se emite, no en la pagina.

### D4. Cada observador tiene su goroutine, y su lentitud ya esta acotada por el bus

El bus entrega a cada suscriptor por un canal con buffer propio y descarte del cliente lento (D4 de
`01-add-relay-event-bus`). Esta capacidad no agrega ninguna proteccion nueva: la hereda.

Lo que si agrega es el limite de observadores simultaneos y la liberacion al cerrarse la conexion.
El cierre se detecta por el contexto de la peticion, que se cancela cuando el cliente corta; ahi se
cancela la suscripcion y se libera el lugar. Sin eso, abrir y cerrar el monitor repetidamente
llenaria el cupo con observadores que ya no existen.

### D5. El limite de observadores se comprueba contra el bus, no contra un contador propio

El bus ya sabe cuantos suscriptores tiene. Llevar un contador aparte en el manejador abre la
posibilidad de que los dos discrepen —una suscripcion que se cancela sin decrementar, por ejemplo—
y el sintoma seria un monitor que dice estar lleno sin estarlo.

### D6. Las cabeceras de intercambio entre origenes son de todo el servicio, no del dashboard

Se aplican como una envoltura sobre el ruteo, no dentro de cada manejador: si fueran por ruta,
agregar una ruta nueva significaria acordarse de agregarlas, y olvidarse no produce ningun error
visible hasta que un navegador falla.

Cerradas por defecto, o sea sin emitir ninguna cabecera mientras no haya origenes configurados, que
es como se comporta el servicio hoy. La consulta previa del navegador se responde en la envoltura y
**no llega al manejador**: sin eso, una consulta previa sobre el relay terminaria relayando.

### D7. El registro de las rutas es condicional, no el manejador

Con el dashboard apagado las rutas **no se registran**. La alternativa —registrarlas siempre y que
el manejador responda que estan deshabilitadas— deja una superficie que informa que el monitor
existe, y hace que un `GET` con el metodo equivocado responda `405` en lugar de comportarse como
cualquier path desconocido.

Consecuencia deliberada: con el dashboard apagado, esos paths caen al catch-all JSON-RPC, igual que
cualquier otro path sin manejador. Es lo que dice el requisito modificado de `relay-http-routing`.

## Risks / Trade-offs

| Riesgo | Mitigacion |
|---|---|
| La reanudacion pierde un evento publicado justo al conectarse | D1: replay y suscripcion bajo el mismo candado, con un test que publica concurrentemente mientras un observador se conecta |
| Un observador que no lee frena el relay | Ya lo cubre el descarte del cliente lento del bus; el test de esta capacidad verifica que el tiempo de respuesta no dependa de cuantos observadores haya |
| Abrir y cerrar el monitor filtra suscripciones y llena el cupo | Cancelacion atada al contexto de la peticion, verificada abriendo y cerrando muchas veces y comprobando que el cupo vuelve a estar libre |
| La pagina portada deja de pintar sin error visible | El contrato de eventos esta congelado y tiene test de contrato en `01`. La pagina no se modifica al portarla, asi que una divergencia solo puede venir de lo que se emite |
| Un proxy retiene el stream y no llega nada, aunque todo funcione | Cabecera que pide no almacenar ni transformar, mas el latido periodico. Es el fallo mas confuso de diagnosticar, porque el servicio se ve sano |
| Abrir el intercambio entre origenes expone el relay a cualquier pagina | Cerrado por defecto; habilitarlo es una decision explicita del operador, documentada junto con que el servicio no autentica |

## Migration Plan

No hay migracion. El despliegue es reemplazar el binario:

1. Con la configuracion existente el dashboard queda apagado y no hay ninguna ruta ni cabecera
   nueva. El servicio se comporta igual.
2. Para habilitarlo, `dashboard.enabled = true`. Las rutas quedan en el puerto del servicio y **no
   son alcanzables por el puerto 80**: el nginx del writer enruta por metodo leyendo el cuerpo, asi
   que un `GET /dashboard` por el 80 termina en besu. Abrirlo exige tocar `besu-networks`, aparte.
3. Rollback: reinstalar el binario anterior. No hay estado persistido.

## Open Questions

- **Capacidad del canal por observador.** `01-add-relay-event-bus` la dejo abierta a proposito,
  para ajustarla midiendo contra el dashboard real. Esta capacidad es ese dashboard, asi que el
  valor se puede revisar aca; no afecta a ningun requisito y el descarte del cliente lento funciona
  con cualquier valor razonable.
