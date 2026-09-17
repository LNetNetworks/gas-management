## Context

Ver `proposal.md` para el por que. El estado de partida, que hace que este cambio sea chico:

- `service/prepare.go:107` (`PrepareMetaTx`) ya es **la puerta compartida**: decodifica, valida y
  emite `relay.received` y `relay.decoded` para las dos puertas, y devuelve un `Rejection` con los
  dos vocabularios -codigo numerico JSON-RPC y simbolico REST- de modo que ninguna puede dar un
  motivo distinto que la otra. Ver D4 de 02.
- `service/prepare.go:28-36`: el catalogo de codigos, con la regla escrita de no inventar codigos
  fuera del catalogo de referencia. Este cambio agrega tres de los que hoy faltan.
- `service/metatx.go:55` (`DecodeGasModelSuffix`) ya lee `nodeAddress`, `expiration`, `innerData` y
  el selector, **solo para registrarlos**, y su comentario dice que validarlos es otra propuesta.
- `service/prepare.go:149-159`: el chequeo de permisos sobre el sender ya es cerrado ante fallo
  (`PERMISSIONING_UNAVAILABLE`) y ya esta detras de `security.permissionsEnabled`.
- `service/relaySignerService.go:464` (`VerifySender`): abre un cliente RPC, lee la direccion fija
  de `security.accountContractAddress` y consulta `accountPermitted`. Sin cache: una consulta a la
  cadena por metatx.
- `service/info.go:34-41`: los campos que hoy salen fijos -`accountRulesSource` siempre nulo o
  `config`, `nodePermitted` nulo, `minExpirationSeconds` y `expirationToleranceSeconds` en `0`-.

Restriccion: `go-ethereum v1.9.15`. La lectura del registro de permisos es una llamada de contrato
de una sola funcion, asi que no hacen falta bindings generados nuevos.

## Goals / Non-Goals

**Goals:**

- Rechazar antes de gastar una transaccion del writer node lo que el sufijo ya dice que va a fallar.
- Que el permisionado se resuelva contra la cadena sin romper el despliegue que hoy usa una
  direccion fija.
- Que con los flags apagados el conjunto de metatx aceptadas sea **exactamente** el de hoy, y que
  eso se verifique contra el binario anterior.

**Non-Goals:**

- El pre-chequeo por simulacion `eth_call`. Comparte la idea -no gastar un bloque para descubrir un
  rechazo- pero es otra brecha, con su propio flag y su propio codigo.
- Reemplazar el cliente RPC por request por uno compartido.
- Validar nada mas del `data` de la metatx: lo que este cambio valida es el sufijo del modelo de
  gas, no la llamada del usuario.

## Decisions

### D1. Las validaciones van en la puerta compartida, justo despues de `relay.decoded`

`PrepareMetaTx` ya garantiza que las dos puertas acepten y rechacen lo mismo. Las validaciones se
agregan ahi y **despues** de emitir `relay.decoded`: una metatx rechazada por su sufijo deja primero
la traza de lo que traia, y despues la del rechazo. Al reves, el rechazo apareceria sin que se pueda
ver contra que se evaluo.

Orden dentro de la puerta, de lo barato a lo caro:

```
  decodificar  ->  relay.decoded  ->  nodeAddress  ->  expiration  ->  permisos  ->  ...
                                      (local)         (local)         (consulta la cadena)
```

Las dos validaciones nuevas son locales: no cuestan ninguna llamada al nodo. Ponerlas antes del
chequeo de permisos evita pagar una consulta a la cadena por una metatx que ya se sabe invalida.

### D2. Un sufijo ausente o ilegible no rechaza: se conserva D13 de 01

El decodificador informa si pudo leer el sufijo. Si no pudo -`data` mas corto, o una expiracion que
no entra en un entero-, las validaciones **no se aplican**. Lo que se valida es un sufijo presente y
legible que dice algo inaceptable.

Rechazar ahi cambiaria el conjunto de metatx aceptadas por una razon distinta de la que este cambio
persigue, y romperia a un cliente que hoy relaya sin sufijo.

### D3. Los defaults son `false`, al reves que en el relayer de referencia

Node valida por defecto (`ENFORCE_NODE_ADDRESS` y `ENFORCE_EXPIRATION` en `true`). Aca no: la
invariante del repositorio es que ninguna funcionalidad portada se activa sin opt-in explicito, y
esta cambia **que metatx se aceptan**, que es lo mas sensible que se puede cambiar sin avisar.

Consecuencia en `/info`: con la exigencia apagada, el minimo y la tolerancia se informan como no
vigentes en lugar de publicar sus numeros. Publicar un minimo que no se aplica haria que un cliente
firme para cumplir una regla inexistente, y despues se encuentre con otra cuando se encienda.

### D4. La direccion configurada gana; el registro de la red es el respaldo

| Situacion | Que se usa | Fuente informada |
|---|---|---|
| Hay direccion configurada | esa | `config` |
| No hay, y la red publica una | la publicada | `ingress` |
| No hay, y la red no publica ninguna | ninguna: sin permisionado | sin valor |

Es la precedencia del relayer de referencia y, ademas, lo que hace que un despliegue actual no
cambie de comportamiento al actualizar el binario: hoy todos tienen la direccion escrita en
`config.toml`, asi que el registro de la red no se consulta.

La direccion del registro no trae default: en el relayer de referencia el default es la direccion
conocida de LACChain, pero aca un default no vacio consultaria la cadena en un arranque que hoy no
lo hace. Se configura quien la quiera.

### D5. El registro se resuelve una vez al arrancar; el permiso se cachea por cuenta

La resolucion del contrato de reglas es una propiedad de la red, no de la peticion: se hace al
arrancar y se guarda. El resultado de `accountPermitted` se cachea por cuenta con vigencia
configurable.

El intercambio es explicito y es el mismo que hace el relayer de referencia: sin cache, cada metatx
con el chequeo encendido paga una consulta a la cadena; con cache, un alta tarda hasta la vigencia
en verse. Por eso la vigencia se configura y se publica en `/info`.

| Alternativa | Por que no |
|---|---|
| Resolver el registro en cada metatx | dos consultas a la cadena por metatx para leer algo que no cambia |
| Cachear tambien el "no permitido" para siempre | una cuenta recien dada de alta no podria relayar hasta reiniciar |

### D6. La comprobacion del nodo al arrancar no aborta

Se consulta si el nodo que relaya esta permitido y se informa en `/info`. Un nodo no permitido, o
una consulta que falla, **no** impiden arrancar: la invariante del servicio es que ningun fallo
externo tumba el proceso, y un binario que no levanta diagnostica peor que uno que arranca e informa
el problema en una ruta que ya existe.

Es la diferencia deliberada con el relayer de referencia, que si aborta cuando se le exige el
permisionado y no lo puede resolver.

### D7. El fallo al leer el allowlist sigue siendo cerrado

Ya lo es hoy (`PERMISSIONING_UNAVAILABLE`) y no cambia. Se deja escrito porque la cache abre la
tentacion de servir un valor viejo cuando la cadena no responde: no se hace. Un permiso vencido se
vuelve a consultar, y si esa consulta falla, se rechaza.

### D8. La expiracion se evalua contra un solo instante por metatx

El momento se toma una vez y se usa para las dos comprobaciones -vencida, y ventana minima-, y viaja
en el mensaje del rechazo junto con la expiracion que traia. Releer el reloj entre una y otra abre
una ventana en la que la metatx esta vencida para una comprobacion y no para la otra, y produce un
mensaje que no se puede reproducir.

El limite efectivo es `minimo - tolerancia`, acotado a cero: una tolerancia mayor que el minimo no
puede volverse un limite negativo.

## Risks / Trade-offs

- **Rechazar de mas**: es el riesgo central, y toda metatx rechazada de mas es una que hoy funciona
  → defaults apagados, la tolerancia sobre el minimo, y la verificacion contra el binario anterior
  con los flags en `false`.
- **El reloj del proceso corrido respecto del de la red** haria rechazar metatx validas por
  expiracion → la tolerancia lo absorbe en el orden de segundos, y el mensaje del rechazo incluye el
  instante evaluado para que el diagnostico sea inmediato.
- **Un alta de cuenta tarda en verse** por la cache → vigencia configurable, publicada en `/info`, y
  documentada como intercambio.
- **Una red cuyo registro de permisos publica una direccion equivocada** dejaria de permitir a todos
  → se informa la fuente en `/info`, y la direccion configurada sigue teniendo precedencia para
  poder forzarla.
- **Una consulta mas al arrancar** (el permiso del nodo) → no bloquea el arranque y no se repite.

## Migration Plan

1. Se despliega con todo apagado: `validation.enforceNodeAddress = false`,
   `validation.enforceExpiration = false` y sin direccion de registro configurada. El binario nuevo
   acepta y rechaza exactamente las mismas metatx que el anterior, y eso se verifica antes de
   encender nada.
2. Se enciende primero `enforceNodeAddress`: es una comparacion local y su unico modo de fallo es
   una metatx que de verdad apunta a otro nodo.
3. Se enciende despues `enforceExpiration`, revisando en el log cuantas metatx habrian sido
   rechazadas antes de exigirlo.
4. La resolucion por el registro de la red se prueba quitando la direccion configurada en un
   despliegue de prueba y comparando `accountRulesAddress` de `/info` contra la que estaba escrita.
5. Rollback: apagar el flag correspondiente. No hay estado persistido.

## Open Questions

- Si conviene descartar lo cacheado de una cuenta al verla rechazada por permisos, para que un alta
  inmediata se refleje sin esperar la vigencia. No cambia ningun requisito: es una optimizacion de
  la cache, y la vigencia configurable ya acota el peor caso.
- Si `minExpirationSeconds` deberia tener un valor distinto del del relayer de referencia para esta
  red. Se adopta `300` con tolerancia `2` porque es lo medido alli; ajustarlo es configuracion, no
  cambio de comportamiento.
