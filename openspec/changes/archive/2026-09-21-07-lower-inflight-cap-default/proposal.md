## Why

`maxInflightPerUser` viene con default 16 y el techo real de la red es 5: Besu limita cuantas
transacciones pendientes acepta de UNA cuenta (`tx-pool-limit-by-account-percentage`, ~5 con los
defaults) y esa cuenta es la del writer node, remitente de TODAS las envolventes. El default esta
tres veces por encima de un techo que no puede superar.

No cuesta throughput -no hay throughput que ganar-, pero degrada los rechazos. Medido en el nodo de
pruebas `34.69.184.205` el 2026-09-21 con el binario `v1.1.0-RC2-50-g69907ae`, rafagas de 12 metatx
de un usuario, cuatro corridas por valor:

| `maxInflightPerUser` | Minadas | Rechazos por cupo | Por Besu | `BAD_NONCE` |
|---|---|---|---|---|
| 5 | 5, 5, 5, 5 | 6, 7, 6, 7 | 1, 0, 1, 0 | **0, 0, 0, 0** |
| 6 | 5, 5, 5, 5 | 6, 6, 5, 5 | 1, 1, 1, 1 | 0, 0, 1, 1 |
| 7 | 5, 5, 5, 5 | 4, 4, 5, 4 | 1, 1, 1, 1 | 2, 2, 1, 2 |
| 16 (default) | 5 | — | 1 | 6 |

Las minadas no se mueven: **5 en todas las corridas, con cualquier cupo**. Lo que se mueve es como
muere lo que sobra. Cada `BAD_NONCE` de esa ultima columna es una metatx que el servicio admitio,
retuvo la ventana entera -3000 ms- y recien entonces rechazo con un motivo que no es el real: el
nonce estaba bien, lo que paso es que la red no daba mas. Con el cupo al ras del techo eso no pasa
nunca: lo que no cabe se rechaza en la puerta, en ~0,7 s, con `TOO_MANY_INFLIGHT`, que si explica
lo ocurrido. Con 16 el cliente espera ~3,8 s para recibir un diagnostico equivocado.

Bajarlo recien ahora es posible. Antes de `06-fix-inflight-eviction-by-nonce` el cupo descartaba por
orden de llegada, asi que ponerlo al ras del techo rompia cadenas y rendia 2,7 minadas de media
contra 5,0. Con el descarte por nonce, bajarlo dejo de ser un castigo: es el efecto secundario que
ese change anticipaba, y esta propuesta lo cobra.

## What Changes

- `reorder.maxInflightPerUser` pasa de `16` a `5` por defecto.
- La documentacion de la clave explica el criterio -igualar el cupo al techo de la red- y no el
  numero, que es circunstancial: si los validadores suben
  `tx-pool-limit-by-account-percentage`, el cupo los sigue.

No es **BREAKING**: la clave conserva nombre y significado, el conjunto de codigos de error no
cambia y el contrato de `POST /` tampoco. Solo afecta a un despliegue que haya encendido
`[reorder] enabled` -que viene apagado- y no haya fijado la clave. Para ese caso el efecto es que
el rechazo llega antes y con el motivo correcto, con las mismas metatx minadas.

## Capabilities

### New Capabilities

Ninguna.

### Modified Capabilities

- `relay-runtime-configuration`: la tabla de valores por defecto dice `16` para
  `reorder.maxInflightPerUser`. Pasa a decir `5`.

## Impact

- `model/RuntimeConfig.go` — `DefaultReorderMaxInflightPerUser`.
- `config.toml` y `README.md` — el valor de ejemplo y el criterio para elegirlo.
- Sin cambios en la superficie HTTP. `GET /info` sigue informando el valor vigente, que para un
  despliegue sin la clave pasa a ser 5.
- Un despliegue que quiera el comportamiento anterior fija `maxInflightPerUser = 16` y no cambia
  nada mas.
