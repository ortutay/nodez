$(document).init(function() {
    var conn = new WebSocket('ws://' + location.host + '/nodez/wire/stream');

    conn.onmessage = handleWireStreamMessage
});


function handleWireStreamMessage(e) {
    d = $.parseJSON(e.data)

    console.log(d)

    switch (d["Command"]) {
    case "inv":
        for (var i = 0; i < d["Inv"].length; i++) {
            item = d["Inv"][i]
            el = $("#messageStream")
                .prepend("<div/>", item["Hash"]);
        }
        break;
    }
}
