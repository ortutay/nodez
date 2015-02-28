$(document).init(function() {
    var wire = new WebSocket('ws://' + location.host + '/nodez/wire/stream');
    var info = new WebSocket('ws://' + location.host + '/nodez/info/stream');

    wire.onmessage = handleWireStreamMessage
    info.onmessage = handleInfoStreamMessage
});


function handleWireStreamMessage(e) {
    d = $.parseJSON(e.data)

    console.log(d)

    switch (d.command) {
    // case "sync":
    //     item = d.snc
    //     el = $("#messageStream").prepend("<div class='message-stream-item'>" + item.hash + "</div>");

    case "inv":
        for (var i = 0; i < d.inv.length; i++) {
            item = d.inv[i]
            el = $("#messageStream").prepend("<div class='message-stream-item'>" + item.hash + "</div>");
        }
        break;
    }
    // Prune messages to avoid using too much memory. CSS overflow is used
    // for styling.
    var items = $("#messageStream .message-stream-item");
    for (var i = items.length; i > 500; i--) {
        items.eq(i).remove();
    }
}

function handleInfoStreamMessage(e) {
    d = $.parseJSON(e.data);

    console.log("info: ", d);

    $("#addr").html(d.ip + ":" + d.port);
    if (d.testnet) {
        $("#net").html("testnet");
    } else {
        $("#net").html("mainnet");
    }
    $("#height").html(d.height);
    $("#difficulty").html(d.difficulty);
    $("#hashesPerSec").html(d.hashesPerSec);
    $("#connections").html(d.connections);
    $("#bytesRecv").html(d.bytesRecv);
    $("#bytesSent").html(d.bytesSent);
}
