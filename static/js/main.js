$(document).init(function() {
    var wire = new WebSocket('ws://' + location.host + '/nodez/wire/stream');
    var info = new WebSocket('ws://' + location.host + '/nodez/info/stream');

    wire.onmessage = handleWireStreamMessage
    info.onmessage = handleInfoStreamMessage
});


function handleWireStreamMessage(e) {
    d = $.parseJSON(e.data)

    console.log(d)

	  datestr = "3 seconds ago";

    switch (d.command) {
    case "sync":
        return;
        // item = d.snc
        // el = $("#messageStream").prepend("<div class='message-stream-item'>" + item.hash + "</div>");

    case "tx":
				el = $("#messageStream").prepend(
            "<div class='message-stream " + d.command + "'>" +
                "<div class='message-stream-left'>" +
                    "<div class='message-stream-cmd'>transaction</div>" +
                    "<div class='message-stream-datetime'>" + datestr + "</div>" +
                "</div>" +
                "<div class='message-stream-main'>" +
                    "<div class='message-stream-tx-details'>" +
                        d.tx.outputsValue/1e8 + " BTC" +
                    "</div>" +
                    "<div class='message-stream-hash'>" + d.tx.hash + "</div>" +
                "</div>" +
            "</div>");
        break;

    case "inv":
        for (var i = 0; i < d.inv.length; i++) {
            item = d.inv[i]
            el = $("#messageStream").prepend(
                "<div class='message-stream " + d.command + "'>" + 
                "<div class='message-stream-cmd'>" + item.type + "</div>" +
                "<div class='message-stream-hash'>" +item.hash + "</div>" +
                "</div>");
        }
        break;

    case "addr":
        for (var i = 0; i < d.addresses.length; i++) {
            addr = d.addresses[i]
            el = $("#messageStream").prepend(
                "<div class='message-stream'>" + 
                "<div class='message-stream-cmd'>" + d.command + "</div>" +
                "<div class='message-stream-addr'>" +
                    addr.ip + ":" + addr.port +
                "</div>" +
                "</div>");
        }
        break;

    default:
        el = $("#messageStream").prepend(
            "<div class='message-stream'>" + 
            "<div class='message-stream-cmd'>" + d.command + "</div>" +
            "<div class='message-stream-msg'>" + d.message + "</div>" +
            "</div>");
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

    // console.log("info: ", d);

    $("#addr").html(d.ip + ":" + d.port);
    if (d.testnet) {
        $("#net").html("testnet");
    } else {
        $("#net").html("mainnet");
    }
    $("#height").html(numberWithCommas(d.height));
    $("#difficulty").html(numberWithCommas(Math.round(d.difficulty)));
    $("#hashesPerSec").html(numberWithCommas(Math.round(d.hashesPerSec/1e9)) + " GH/sec");
    $("#connections").html(numberWithCommas(d.connections));
    $("#bytesRecv").html(numberWithCommas(d.bytesRecv) + " bytes");
    $("#bytesSent").html(numberWithCommas(d.bytesSent) + " bytes");
}

function numberWithCommas(x) {
    var parts = x.toString().split(".");
    parts[0] = parts[0].replace(/\B(?=(\d{3})+(?!\d))/g, ",");
    return parts.join(".");
}
