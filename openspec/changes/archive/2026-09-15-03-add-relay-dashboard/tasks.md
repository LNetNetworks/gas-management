## 1. Reanudacion atomica en el bus

- [x] 1.1 Agregar al bus una operacion que copie lo retenido posterior a un `seq` y registre la
  suscripcion bajo el mismo candado, segun D1, y verificar con un test que publicar concurrentemente
  mientras un observador se conecta no pierde ni duplica ningun evento
- [x] 1.2 Verificar con un test que `Replay` y `Subscribe` siguen funcionando por separado como
  antes, para no romper a ningun consumidor existente

## 2. Ruteo condicional

- [x] 2.1 Registrar las rutas del monitor solo cuando el dashboard esta habilitado, segun D7, y
  verificar con un test que con el flag apagado esos paths los atiende el camino JSON-RPC como
  cualquier path desconocido, sin responder `405`
- [x] 2.2 Verificar con un test que con el flag encendido las dos rutas responden, y que pedirlas
  con un metodo que no les corresponde responde `405` sin caer al camino JSON-RPC
- [x] 2.3 Verificar con un test que ninguna ruta que ya existia cambio de comportamiento al
  agregarse el registro condicional

## 3. Entrega de la pagina

- [x] 3.1 Portar la pagina del relayer de Node a un `index.html` sin modificar su contenido, y
  verificar comparando que el HTML, el CSS y el JavaScript son los mismos que los de la fuente
- [x] 3.2 Embeber la pagina en el binario y servirla como HTML, y verificar con un test que se
  responde sin leer ningun archivo del sistema de archivos
- [x] 3.3 Verificar que el binario compilado sirve la pagina desde un directorio vacio, para
  comprobar que no depende de archivos al lado del ejecutable

## 4. Stream de eventos

- [x] 4.1 Responder el stream como flujo de eventos enviados por el servidor, con cada evento
  identificado por su numero de secuencia, y verificar con un test que un evento publicado con el
  stream abierto llega al observador
- [x] 4.2 Aceptar el punto de partida indicado en la peticion y el que manda el reintento automatico
  del navegador, y verificar con tests que se reanuda desde el numero indicado por cualquiera de los
  dos
- [x] 4.3 Entregar el historial y despues lo nuevo usando la operacion atomica de la seccion 1, y
  verificar con un test que un evento publicado en el instante de conectarse llega exactamente una
  vez
- [x] 4.4 Verificar con tests los dos casos restantes de reanudacion: sin indicar punto de partida
  se entrega todo lo retenido, y desde un punto ya descartado se entrega todo lo que queda sin error
- [x] 4.5 Emitir el latido periodico y las cabeceras que piden no almacenar ni transformar la
  respuesta, y verificar con tests que un periodo sin eventos mantiene el stream abierto y que las
  cabeceras estan presentes

## 5. Limite de observadores y liberacion

- [x] 5.1 Rechazar la conexion que supera el limite de observadores indicando el motivo, y verificar
  con un test que los ya conectados siguen recibiendo eventos y que ninguno es desplazado
- [x] 5.2 Cancelar la suscripcion y liberar el lugar cuando la conexion se cierra, atado al contexto
  de la peticion segun D4, y verificar con un test que abrir y cerrar el stream muchas veces no
  acumula observadores y que el cupo vuelve a estar disponible
- [x] 5.3 Comprobar el limite contra la cantidad de suscriptores del bus y no contra un contador
  propio, segun D5, y verificar con un test que tras liberarse un lugar una conexion nueva se acepta

## 6. Intercambio entre origenes

- [x] 6.1 Aplicar las cabeceras como una envoltura sobre el ruteo y no dentro de cada manejador,
  segun D6, y verificar con un test que alcanzan a todas las rutas incluida una agregada despues
- [x] 6.2 No emitir ninguna cabecera mientras no haya origenes configurados, y verificar con un test
  que la respuesta es identica a la de antes de esta capacidad
- [x] 6.3 Autorizar unicamente a los origenes configurados, y verificar con tests que un origen
  configurado recibe la autorizacion con los metodos y cabeceras aceptados, y que uno no configurado
  no la recibe pero su peticion se procesa igual
- [x] 6.4 Responder la consulta previa del navegador en la envoltura sin llegar al manejador, y
  verificar con un test que una consulta previa sobre el relay no relaya ninguna metatx
- [x] 6.5 Verificar con un test que el cuerpo y el codigo de las respuestas existentes no cambian al
  configurar origenes

## 7. Verificacion de cierre

- [x] 7.1 Correr `go test ./... -race` y verificar que pasa, con atencion a la conexion y
  desconexion concurrente de observadores
- [x] 7.2 Verificar que relayar una metatx con varios observadores conectados devuelve la misma
  respuesta y en un tiempo que no depende de cuantos haya
- [x] 7.3 Verificar contra el binario anterior que las respuestas de `POST /` y de las rutas ya
  existentes son identicas, con el dashboard apagado y con el dashboard encendido
- [x] 7.4 Abrir el monitor contra el servicio en ejecucion, lanzar una rafaga de metatx y verificar
  que la pagina pinta cada una con su estado, sin huecos y sin errores en la consola del navegador
- [x] 7.5 Verificar que el servicio arranca con el `config.toml` de una instalacion previa y que el
  monitor queda apagado sin necesidad de configurar nada
